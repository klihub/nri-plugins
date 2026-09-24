// Copyright The NRI Plugins Authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Names inside a job directory.
const (
	jobRequest = "request"
	jobPid     = "pid"
	jobStarted = "started"
	jobStatus  = "status"
	jobLog     = "log"
)

// Temporary names for the two files written by rename. Dotted by the same
// convention as everywhere else here, and derived from their targets so renaming
// one cannot leave the other behind.
//
// What actually keeps them out of the way is not the dot: ignoredName is consulted
// for the queue directory and for hook directories, never inside a job. It is
// allJobs skipping anything which is not a directory, and readJob asking for one
// name rather than listing.
var (
	tempRequest = "." + jobRequest
	tempPid     = "." + jobPid
)

// jobNamePattern is what a suggested job name has to match to be used as one.
//
// Hoisted out of makeJobDir because compiling it per request buys nothing, and a
// leading dash is refused as well as any character outside the set: the name
// becomes the run's --name argument and through it RESULT_NAME, and a request is
// free to suggest name=-anything.
var jobNamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)

// Job is an accepted request, with an identity and somewhere to keep it.
type Job struct {
	ID      string
	Dir     string
	Request *Request
}

// errBadRequest marks a request whose own contents are why it cannot be accepted,
// as against a failure of ours which a later tick could get past.
//
// The distinction decides what happens to the work: admit sets a bad request aside
// for good, because nothing retries it, and retries everything else. Without it a
// full disk or a file field pointing at something not written yet would look the
// same as nonsense, and work somebody asked for would be thrown away.
var errBadRequest = errors.New("bad request")

// acceptRequest takes a request out of the queue and makes a job of it.
//
// The queue entry is removed only once the job is complete, so a loser cleans up
// after itself. A crash in that window can leave a duplicate job for the sweep.
func acceptRequest(cfg *Config, reqPath string) (*Job, error) {
	data, err := os.ReadFile(reqPath)
	if err != nil {
		return nil, err
	}
	req, err := parseRequest(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %w", filepath.Base(reqPath), errBadRequest, err)
	}

	suggested, _ := req.Get("name")
	id, dir, err := makeJobDir(cfg, suggested)
	if err != nil {
		return nil, err
	}

	job := &Job{ID: id, Dir: dir, Request: req}

	// Copy in anything the request only points at, so that the job is a complete
	// account of what was asked for and replaying it needs nothing else.
	for _, field := range req.fileFields() {
		copied := filepath.Join(dir, strings.ToLower(field.Name))
		if err := copyFile(field.Value, copied); err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("%s: %w", field.Name, err)
		}
		req.Set(field.Name, copied)
	}

	req.Set("job", id)
	req.Set("queued_at", strconv.FormatInt(time.Now().Unix(), 10))

	// Written to a temporary name and renamed, because a truncated request can
	// parse cleanly and leave a job whose description of the work is quietly
	// missing lines, which is worse than one that obviously fails to read.
	if err := os.WriteFile(filepath.Join(dir, tempRequest), req.Bytes(), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := renameIn(dir, tempRequest, jobRequest, true); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := os.Remove(reqPath); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	return job, nil
}

// makeJobDir creates a job directory, with the name asked for when it is free.
//
// The orchestrator names jobs because it is the only thing which knows what else
// is live. The runner names a run after the minute it started and puts its
// worktree under that name, so two runs in one minute would share both.
func makeJobDir(cfg *Config, suggested string) (string, string, error) {
	if err := os.MkdirAll(cfg.jobsDir(), 0o755); err != nil {
		return "", "", err
	}

	base := suggested
	if base == "" || !jobNamePattern.MatchString(base) {
		base = time.Now().UTC().Format("2006-01-02-15-04-05")
	}

	for attempt := 0; ; attempt++ {
		id := base
		if attempt > 0 {
			id = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		dir := filepath.Join(cfg.jobsDir(), id)

		// Check if the name is taken in either jobs/ or done/.
		if _, err := os.Stat(filepath.Join(cfg.doneDir(), id)); !errors.Is(err, fs.ErrNotExist) {
			if err != nil {
				return "", "", err
			}
			// Name exists in done/, try the next attempt.
			if attempt > 100 {
				return "", "", fmt.Errorf("cannot find a free name for a job called %q", base)
			}
			continue
		}

		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return id, dir, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", "", err
		}
		if attempt > 100 {
			return "", "", fmt.Errorf("cannot find a free name for a job called %q", base)
		}
	}
}

// readJob reads back a job from its directory.
func readJob(dir string) (*Job, error) {
	data, err := os.ReadFile(filepath.Join(dir, jobRequest))
	if err != nil {
		return nil, err
	}
	req, err := parseRequestStored(data)
	if err != nil {
		return nil, err
	}

	return &Job{ID: filepath.Base(dir), Dir: dir, Request: req}, nil
}

// parseRequestStored reads a request we wrote ourselves, which carries the keys a
// submitted one may not.
func parseRequestStored(data []byte) (*Request, error) {
	req := &Request{}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("%q is not a field", line)
		}
		req.Set(name, value)
	}

	if len(req.Fields) == 0 {
		return nil, fmt.Errorf("no fields, so nothing describes this job")
	}

	return req, nil
}

// allJobs is every job directory, whether or not its request still reads.
//
// A directory whose request is unreadable still matters: alive() needs only the
// pid file, and a running job dropped from the count is a second job admitted on
// top of it. Carry an empty request rather than skipping, so Key() is empty and
// nothing dereferences nil.
func allJobs(cfg *Config) ([]*Job, error) {
	entries, err := os.ReadDir(cfg.jobsDir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var jobs []*Job
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(cfg.jobsDir(), entry.Name())
		job, err := readJob(dir)
		if err != nil {
			// A directory with no request yet is one acceptRequest is still
			// building, not a corrupt job. Only say something about corruption.
			if !errors.Is(err, fs.ErrNotExist) {
				warnf("%s: %v", entry.Name(), err)
			}
			job = &Job{ID: entry.Name(), Dir: dir, Request: &Request{}}
		}
		jobs = append(jobs, job)
	}

	return jobs, nil
}

// liveJobs are the jobs whose supervising process is still there.
func liveJobs(cfg *Config) ([]*Job, error) {
	all, err := allJobs(cfg)
	if err != nil {
		return nil, err
	}

	var live []*Job
	for _, job := range all {
		if job.alive() {
			live = append(live, job)
		}
	}

	return live, nil
}

// recordPid writes which process is supervising this job, and when that process
// started, so the pid can be trusted later.
func (j *Job) recordPid(pid int) error {
	started, err := procStartTime(pid)
	if err != nil {
		return err
	}

	temp := filepath.Join(j.Dir, tempPid)
	if err := os.WriteFile(temp, fmt.Appendf(nil, "%d %d\n", pid, started), 0o644); err != nil {
		return err
	}

	// Renamed into place, because a half-written pid file reads as a dead job and
	// the sweep would then treat a running job as abandoned. Overwriting is right
	// here: re-recording a pid for the same job should replace what is there.
	return renameIn(j.Dir, tempPid, jobPid, true)
}

// alive tells whether this job's supervising process is still running.
//
// Both halves are needed. A pid on its own says nothing once it has been reused,
// and a job wrongly believed live holds a slot for good.
func (j *Job) alive() bool {
	fields := strings.Fields(string(readFileOrEmpty(filepath.Join(j.Dir, jobPid))))
	if len(fields) != 2 {
		return false
	}

	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid < 2 {
		return false
	}
	recorded, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return false
	}

	current, err := procStartTime(pid)
	if err != nil {
		return false
	}

	return current == recorded
}

// markStarted records that the work itself began, which is what tells a job worth
// retrying from one which is not.
func (j *Job) markStarted() error {
	return os.WriteFile(filepath.Join(j.Dir, jobStarted), []byte("\n"), 0o644)
}

func (j *Job) started() bool {
	_, err := os.Stat(filepath.Join(j.Dir, jobStarted))

	return err == nil
}

// finish records the outcome and moves the job out of the way.
func (j *Job) finish(cfg *Config, status int) error {
	if err := os.WriteFile(filepath.Join(j.Dir, jobStatus),
		fmt.Appendf(nil, "%d\n", status), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.doneDir(), 0o755); err != nil {
		return err
	}

	moved := filepath.Join(cfg.doneDir(), j.ID)
	if err := os.Rename(j.Dir, moved); err != nil {
		return err
	}
	j.Dir = moved

	if err := pruneDone(cfg); err != nil {
		warnf("pruning %s: %v", cfg.doneDir(), err)
	}

	return nil
}

// requeue puts a job's request back, once.
//
// Once because a request which kills whatever picks it up would otherwise be
// retried on every tick forever.
func (j *Job) requeue(cfg *Config) error {
	if j.started() {
		return fmt.Errorf("%s had started, not queueing it again", j.ID)
	}
	if attempt, _ := j.Request.Get("attempt"); attempt != "" {
		return fmt.Errorf("%s has been tried before, not queueing it again", j.ID)
	}
	// A job whose request never read back has nothing to queue again. Requeueing
	// it would write an entry with no work in it, which admit would accept and
	// start hooks on. allJobs deliberately surfaces such jobs so the cap counts
	// them, which is what makes this reachable.
	if len(j.Request.Fields) == 0 {
		return fmt.Errorf("%s has no readable request, not queueing it again", j.ID)
	}

	// Marked in the job before the queue entry exists, so a crash between the two
	// leaves a job which refuses to be queued again rather than one which is
	// queued twice.
	j.Request.Set("attempt", "2")
	// Written to a temporary name and renamed, because a half-written request is
	// a job nobody can read, and this is the file that says what the work is.
	if err := os.WriteFile(filepath.Join(j.Dir, tempRequest), j.Request.Bytes(), 0o644); err != nil {
		return err
	}
	if err := renameIn(j.Dir, tempRequest, jobRequest, true); err != nil {
		return err
	}

	req := &Request{}
	for _, field := range j.Request.Fields {
		if slices.Contains(reservedKeys, field.Name) {
			continue
		}
		req.Set(field.Name, field.Value)
	}

	if err := os.MkdirAll(cfg.queueDir(), 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
	temp := filepath.Join(cfg.queueDir(), "."+name)
	if err := os.WriteFile(temp, req.Bytes(), 0o644); err != nil {
		return err
	}
	if err := renameIn(cfg.queueDir(), filepath.Base(temp), name, false); err != nil {
		_ = os.Remove(temp)
		return err
	}

	return os.RemoveAll(j.Dir)
}

// pruneDone keeps the newest finished jobs and removes the rest.
func pruneDone(cfg *Config) error {
	entries, err := os.ReadDir(cfg.doneDir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		warnf("%s: %v", cfg.doneDir(), err)
		return nil
	}

	type dirEntry struct {
		name string
		time time.Time
	}
	var dirs []dirEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, dirEntry{entry.Name(), info.ModTime()})
	}
	if len(dirs) <= cfg.DoneKeep {
		return nil
	}

	// Sort by modification time, oldest first.
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].time.Before(dirs[j].time) })

	for _, dir := range dirs[:len(dirs)-cfg.DoneKeep] {
		if err := os.RemoveAll(filepath.Join(cfg.doneDir(), dir.name)); err != nil {
			return err
		}
	}

	return nil
}

// procStartTime is when a process started, in clock ticks since boot.
//
// Field 22 of /proc/<pid>/stat. Field 2 is the command name in parentheses and may
// itself hold spaces and parentheses, so everything is counted from the last close
// parenthesis rather than by splitting the line.
func procStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}

	end := strings.LastIndex(string(data), ")")
	if end < 0 {
		return 0, fmt.Errorf("cannot read /proc/%d/stat", pid)
	}

	fields := strings.Fields(string(data)[end+1:])
	// After the command name the next field is state, so field 22 of the line is
	// the 20th here.
	if len(fields) < 20 {
		return 0, fmt.Errorf("cannot read /proc/%d/stat", pid)
	}

	return strconv.ParseUint(fields[19], 10, 64)
}

func readFileOrEmpty(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	return data
}

// copyFile copies a file, refusing one which is not there.
func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}

	return os.WriteFile(to, data, 0o644)
}

// signalGroup sends a signal to a process group, for a timed out hook.
func signalGroup(pid int, signal syscall.Signal) {
	_ = syscall.Kill(-pid, signal)
}
