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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestAcceptGivesAJobItsOwnDirectory checks that accepting a request moves it out
// of the queue into a job of its own, so a second tick cannot accept it twice.
func TestAcceptGivesAJobItsOwnDirectory(t *testing.T) {
	cfg := testConfig(t)

	path, err := submit(cfg, mustRequest(t, "branch=main\nname=test-run\n"))
	if err != nil {
		t.Fatal(err)
	}

	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != "test-run" {
		t.Errorf("id: got %q, want test-run", job.ID)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the request is still in the queue")
	}
	if _, err := os.Stat(filepath.Join(job.Dir, "request")); err != nil {
		t.Errorf("the job has no request: %v", err)
	}

	left, _ := queued(cfg)
	if len(left) != 0 {
		t.Errorf("queue still holds %v", left)
	}
}

// TestAcceptMakesTheNameUnique checks that a suggested name which is taken does
// not collide.
//
// This is the reason identity is the orchestrator's to assign: the runner names a
// run after the minute it started and puts its worktree under that name, so two
// runs starting in one minute would share both. At a cap of one that cannot
// happen. The moment the cap rises it would, silently.
func TestAcceptMakesTheNameUnique(t *testing.T) {
	cfg := testConfig(t)

	var ids []string
	for range 2 {
		path, err := submit(cfg, mustRequest(t, "branch=main\nname=same\n"))
		if err != nil {
			t.Fatal(err)
		}
		job, err := acceptRequest(cfg, path)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, job.ID)
	}

	if ids[0] == ids[1] {
		t.Errorf("both jobs are called %q", ids[0])
	}
	if ids[0] != "same" {
		t.Errorf("the first job should keep the name asked for, got %q", ids[0])
	}
}

// TestAcceptRefusesANameLikeAnOption checks that a suggested name starting with a
// dash is passed over for the timestamp, rather than becoming the job's identity.
//
// The name reaches the run hook, which hands it to the runner as --name, so a
// request suggesting name=--help or name=-n would be arguing with a command line
// rather than naming a job. Every other character is already whitelisted; the dash
// is whitelisted too, and only its being first is the problem.
func TestAcceptRefusesANameLikeAnOption(t *testing.T) {
	cfg := testConfig(t)

	path, err := submit(cfg, mustRequest(t, "branch=main\nname=-n\n"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	if strings.HasPrefix(job.ID, "-") {
		t.Errorf("job is called %q, which reads as an option", job.ID)
	}
	// And a dash anywhere else is still fine, that being the conventional way to
	// name a run.
	path, err = submit(cfg, mustRequest(t, "branch=main\nname=test-run-2\n"))
	if err != nil {
		t.Fatal(err)
	}
	job, err = acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != "test-run-2" {
		t.Errorf("id: got %q, want test-run-2", job.ID)
	}
}

// TestAcceptCopiesFileFieldsIn checks that a _FILE target is copied into the job
// and the value rewritten, so a producer may write to a temporary file and forget
// it, and so replaying a job needs nothing outside its directory.
func TestAcceptCopiesFileFieldsIn(t *testing.T) {
	cfg := testConfig(t)

	source := filepath.Join(t.TempDir(), "candidates.json")
	if err := os.WriteFile(source, []byte(`[{"n":1}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	path, err := submit(cfg, mustRequest(t, "branch=main\ncandidates_FILE="+source+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	value, _ := job.Request.Get("candidates_FILE")
	if !strings.HasPrefix(value, job.Dir) {
		t.Errorf("value was not rewritten into the job: %q", value)
	}
	if got := string(mustReadFile(t, value)); got != `[{"n":1}]` {
		t.Errorf("copied content: got %q", got)
	}

	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(value); err != nil {
		t.Errorf("the copy did not survive the original going away: %v", err)
	}
}

// TestAcceptRefusesAMissingFileTarget checks that a _FILE naming nothing is
// refused when the job is accepted, rather than at three in the morning inside a
// hook.
func TestAcceptRefusesAMissingFileTarget(t *testing.T) {
	cfg := testConfig(t)

	path, err := submit(cfg, mustRequest(t, "branch=main\nx_FILE=/nowhere/at/all\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acceptRequest(cfg, path); err == nil {
		t.Error("accepted a request naming a file which is not there")
	}
}

// TestLivenessNeedsPidAndStartTime checks that a job is live only while the pid it
// recorded is the process it recorded.
//
// A pid alone is not enough: pids are reused, and a recycled one would make a
// finished job look like it was still going, so nothing would ever be admitted
// again. The start time read from /proc pins which process the pid means.
func TestLivenessNeedsPidAndStartTime(t *testing.T) {
	cfg := testConfig(t)

	path, _ := submit(cfg, mustRequest(t, "branch=main\n"))
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	// A process of our own, which we know is alive.
	sleep := exec.Command("sleep", "60")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sleep.Process.Kill() }()

	if err := job.recordPid(sleep.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if !job.alive() {
		t.Error("a job whose process is running is not alive")
	}

	_ = sleep.Process.Kill()
	_, _ = sleep.Process.Wait()
	if job.alive() {
		t.Error("a job whose process is gone is still alive")
	}

	// The same pid, recorded with a start time which is not that process's.
	if err := os.WriteFile(filepath.Join(job.Dir, "pid"),
		fmt.Appendf(nil, "%d 1\n", os.Getpid()), 0o644); err != nil {
		t.Fatal(err)
	}
	reread, err := readJob(job.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if reread.alive() {
		t.Error("a pid whose start time does not match was taken for live")
	}
}

// TestJobWithNoPidIsNotLive checks that a job which has not recorded a pid yet is
// not live, so a crash between accepting and starting is swept rather than waited
// on forever.
func TestJobWithNoPidIsNotLive(t *testing.T) {
	cfg := testConfig(t)

	path, _ := submit(cfg, mustRequest(t, "branch=main\n"))
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	if job.alive() {
		t.Error("a job with no pid recorded is live")
	}
}

// TestFinishMovesTheJobAndRecordsStatus checks where a finished job goes and that
// its outcome is readable afterwards.
func TestFinishMovesTheJobAndRecordsStatus(t *testing.T) {
	cfg := testConfig(t)

	path, _ := submit(cfg, mustRequest(t, "branch=main\nname=done-one\n"))
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := job.finish(cfg, 3); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(cfg.jobsDir(), "done-one")); !os.IsNotExist(err) {
		t.Error("the job is still under jobs/")
	}
	moved := filepath.Join(cfg.doneDir(), "done-one")
	if job.Dir != moved {
		t.Errorf("job.Dir: got %q, want %q", job.Dir, moved)
	}
	if got := strings.TrimSpace(string(mustReadFile(t, filepath.Join(moved, "status")))); got != "3" {
		t.Errorf("status: got %q, want 3", got)
	}

	live, err := liveJobs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Errorf("a finished job is still live: %v", live)
	}
}

// TestRequeuePutsTheRequestBackOnce checks that a job which never started its work
// goes back in the queue with attempt=2, and that a second requeue refuses.
//
// Once, not forever: a request which kills the orchestrator would otherwise be
// retried on every tick for good.
func TestRequeuePutsTheRequestBackOnce(t *testing.T) {
	cfg := testConfig(t)

	path, _ := submit(cfg, mustRequest(t, "branch=main\n"))
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := job.requeue(cfg); err != nil {
		t.Fatal(err)
	}

	paths, _ := queued(cfg)
	if len(paths) != 1 {
		t.Fatalf("got %d queued, want 1", len(paths))
	}
	again, err := acceptRequest(cfg, paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := again.Request.Get("attempt"); got != "2" {
		t.Errorf("attempt: got %q, want 2", got)
	}
	if err := again.requeue(cfg); err == nil {
		t.Error("a second requeue was allowed")
	}
}

// TestLiveJobsWithCorruptRequestStillCounts checks that a job directory with a
// live pid but a corrupt request file still appears in liveJobs. A running job
// dropped from the cap is a second job admitted on top of it.
func TestLiveJobsWithCorruptRequestStillCounts(t *testing.T) {
	cfg := testConfig(t)

	path, _ := submit(cfg, mustRequest(t, "branch=main\nname=corrupt\n"))
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	// Start a live process and record its pid.
	sleep := exec.Command("sleep", "60")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sleep.Process.Kill() }()

	if err := job.recordPid(sleep.Process.Pid); err != nil {
		t.Fatal(err)
	}

	// Corrupt the request file.
	if err := os.WriteFile(filepath.Join(job.Dir, "request"), []byte("not valid"), 0o644); err != nil {
		t.Fatal(err)
	}

	// liveJobs should still return the job because it is alive.
	live, err := liveJobs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 {
		t.Fatalf("got %d live jobs, want 1", len(live))
	}
	if live[0].ID != "corrupt" {
		t.Errorf("live job ID: got %q, want corrupt", live[0].ID)
	}
}

// TestRequeueMustRefuseStartedJobs checks that a job that has called markStarted
// is refused by requeue, even if it has not yet set attempt.
func TestRequeueMustRefuseStartedJobs(t *testing.T) {
	cfg := testConfig(t)

	path, _ := submit(cfg, mustRequest(t, "branch=main\n"))
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	if err := job.markStarted(); err != nil {
		t.Fatal(err)
	}

	// Requeue should refuse.
	if err := job.requeue(cfg); err == nil {
		t.Error("requeue allowed a started job")
	}
}

// TestRequeueMustRefuseCorruptJobs checks that a job whose request file is
// corrupt is refused by requeue when allJobs surfaces it with an empty Request,
// preventing a corrupt job from being laundered into a queue entry.
func TestRequeueMustRefuseCorruptJobs(t *testing.T) {
	cfg := testConfig(t)

	path, _ := submit(cfg, mustRequest(t, "branch=main\nname=corrupt-requeue\n"))
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the request file so it cannot be parsed.
	if err := os.WriteFile(filepath.Join(job.Dir, "request"), []byte("not valid"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Read the job back through allJobs, which will surface it with an empty Request.
	all, err := allJobs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d jobs, want 1", len(all))
	}
	corruptJob := all[0]

	// Requeue should refuse because the request is empty.
	if err := corruptJob.requeue(cfg); err == nil {
		t.Error("requeue allowed a corrupt job with empty request")
	}

	// No queue entry should have been created.
	queuedPaths, err := queued(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(queuedPaths) != 0 {
		t.Errorf("requeue created a queue entry despite refusing: got %d entries", len(queuedPaths))
	}

	// The job directory should still exist.
	if _, err := os.Stat(corruptJob.Dir); err != nil {
		t.Errorf("a refused requeue removed the job directory: %v", err)
	}
}

// TestActiveKeysCountsRunningJobs checks that a key belonging to a live job is
// active, so the same work is not queued again while it is still going. This
// half of the dedup contract was unexecuted code until jobs became real.
func TestActiveKeysCountsRunningJobs(t *testing.T) {
	cfg := testConfig(t)

	path, _ := submit(cfg, mustRequest(t, "key=origin@main\nbranch=main\n"))
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	// A process of our own, which we know is alive.
	sleep := exec.Command("sleep", "60")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sleep.Process.Kill() }()

	if err := job.recordPid(sleep.Process.Pid); err != nil {
		t.Fatal(err)
	}

	// Submitting a request with the same key should be dropped.
	path2, err := submit(cfg, mustRequest(t, "key=origin@main\nbranch=main\n"))
	if err != nil {
		t.Fatal(err)
	}
	if path2 != "" {
		t.Errorf("submit returned a path when it should have been dropped: %q", path2)
	}

	// activeKeys should contain the key.
	keys, err := activeKeys(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !keys["origin@main"] {
		t.Error("key was not active even though the job is running")
	}
}
