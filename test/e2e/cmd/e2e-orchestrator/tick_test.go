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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// orchestrator builds the binary once so a test can have tick start a real
// supervising process, which is the only way the pid and sweep paths are real.
func orchestrator(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "e2e-orchestrator")
	build := exec.Command("go", "build", "-o", path, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatal(err)
	}

	return path
}

// waitForDone waits for a job to reach done/, which is where the supervisor puts
// it, and returns its status.
func waitForDone(t *testing.T, cfg *Config, id string) string {
	t.Helper()

	status := filepath.Join(cfg.doneDir(), id, jobStatus)
	for range 100 {
		if data, err := os.ReadFile(status); err == nil {
			return strings.TrimSpace(string(data))
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s never finished", id)

	return ""
}

// TestTickRunsAJobThrough checks a request from the queue to done/, with the run
// hook having run and post-run having seen its status.
func TestTickRunsAJobThrough(t *testing.T) {
	cfg := testConfig(t)
	t.Setenv("E2E_ORCHESTRATOR_BIN", orchestrator(t))

	writeHook(t, cfg, eventRun, "50-work", `echo working > "$E2E_JOB_DIR/worked"`)
	writeHook(t, cfg, eventPostRun, "50-after", `echo "$E2E_STATUS" > "$E2E_JOB_DIR/seen"`)

	if _, err := submit(cfg, mustRequest(t, "branch=main\nname=job-one\n")); err != nil {
		t.Fatal(err)
	}
	if err := tick(cfg); err != nil {
		t.Fatal(err)
	}

	if got := waitForDone(t, cfg, "job-one"); got != "0" {
		t.Errorf("status: got %q, want 0", got)
	}
	done := filepath.Join(cfg.doneDir(), "job-one")
	if got := strings.TrimSpace(string(mustReadFile(t, filepath.Join(done, "worked")))); got != "working" {
		t.Errorf("the run hook did not run: %q", got)
	}
	if got := strings.TrimSpace(string(mustReadFile(t, filepath.Join(done, "seen")))); got != "0" {
		t.Errorf("post-run saw status %q", got)
	}
}

// TestTickHonoursTheCap checks that the cap bounds what is started, which at one is
// the same thing the runner's lock did.
func TestTickHonoursTheCap(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxJobs = 2
	t.Setenv("E2E_ORCHESTRATOR_BIN", orchestrator(t))

	writeHook(t, cfg, eventRun, "50-work", `sleep 5`)

	for _, name := range []string{"a", "b", "c"} {
		if _, err := submit(cfg, mustRequest(t, "branch=main\nname="+name+"\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := tick(cfg); err != nil {
		t.Fatal(err)
	}

	live, err := liveJobs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 2 {
		t.Errorf("started %d jobs, cap is 2", len(live))
	}
	left, _ := queued(cfg)
	if len(left) != 1 {
		t.Errorf("%d requests left queued, want 1", len(left))
	}

	for _, job := range live {
		fields := strings.Fields(string(readFileOrEmpty(filepath.Join(job.Dir, jobPid))))
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			signalGroup(pid, syscall.SIGKILL)
		}
	}
}

// TestTickVetoCancels checks that a pre-run veto discards the job without running
// the work, and that the request does not come back. A veto is a decision.
func TestTickVetoCancels(t *testing.T) {
	cfg := testConfig(t)
	t.Setenv("E2E_ORCHESTRATOR_BIN", orchestrator(t))

	writeHook(t, cfg, eventPreRun, "10-no", `exit 1`)
	writeHook(t, cfg, eventRun, "50-work", `touch "$E2E_JOB_DIR/worked"`)

	if _, err := submit(cfg, mustRequest(t, "branch=main\nname=vetoed\n")); err != nil {
		t.Fatal(err)
	}
	if err := tick(cfg); err != nil {
		t.Fatal(err)
	}

	waitForDone(t, cfg, "vetoed")
	if _, err := os.Stat(filepath.Join(cfg.doneDir(), "vetoed", "worked")); err == nil {
		t.Error("the work ran despite the veto")
	}
	if left, _ := queued(cfg); len(left) != 0 {
		t.Errorf("a vetoed request came back: %v", left)
	}
}

// TestSweepRequeuesAJobWhichNeverStarted checks the crash case: a job accepted and
// then abandoned before the work began is safe to queue again, because nothing has
// happened yet that anybody could be reading.
func TestSweepRequeuesAJobWhichNeverStarted(t *testing.T) {
	cfg := testConfig(t)

	path, _ := submit(cfg, mustRequest(t, "branch=main\nname=abandoned\n"))
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	// A pid which is not running, recorded as if it were.
	if err := os.WriteFile(filepath.Join(job.Dir, jobPid), []byte("999999 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := sweep(cfg); err != nil {
		t.Fatal(err)
	}

	left, _ := queued(cfg)
	if len(left) != 1 {
		t.Fatalf("got %d queued, want 1", len(left))
	}
	if _, err := os.Stat(job.Dir); !os.IsNotExist(err) {
		t.Error("the swept job is still under jobs/")
	}
}

// TestSweepDoesNotRetryStartedWork checks that a job whose work had begun is not
// started again. It may have published half of something, and the orchestrator
// cannot know what, so a retry could overwrite what somebody is reading.
func TestSweepDoesNotRetryStartedWork(t *testing.T) {
	cfg := testConfig(t)

	path, _ := submit(cfg, mustRequest(t, "branch=main\nname=half-done\n"))
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := job.markStarted(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job.Dir, jobPid), []byte("999999 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := sweep(cfg); err != nil {
		t.Fatal(err)
	}

	if left, _ := queued(cfg); len(left) != 0 {
		t.Errorf("started work was queued again: %v", left)
	}
	if got := strings.TrimSpace(string(mustReadFile(t,
		filepath.Join(cfg.doneDir(), "half-done", jobStatus)))); got == "0" {
		t.Error("an abandoned job was recorded as a success")
	}
	// And the job's own log says why it stopped. Measured on a live run: a supervisor
	// killed mid-run leaves a log which ends in the middle of the work, and the only
	// account of what happened was in the tick's output, which is not the file the
	// README points an operator at afterwards.
	// Read rather than mustReadFile, so that no log at all and a log which says
	// nothing about the sweep give the same legible failure instead of an ENOENT
	// from a helper which does not know what it was looking for.
	log, err := os.ReadFile(filepath.Join(cfg.doneDir(), "half-done", jobLog))
	if err != nil || !strings.Contains(string(log), "sweep: supervisor gone") {
		t.Errorf("the abandoned job's log does not say why it stopped: %q, %v", log, err)
	}
}

// TestTickIsExclusive checks that a second tick does nothing while a first holds
// the lock, and that it says so rather than failing.
//
// This is what makes the cap mean anything. Without it two overlapping ticks each
// count the live jobs, each see room, and each admit, so a cap of one starts two
// runs. A tick hook may run for E2E_HOOK_TIMEOUT, so overlapping a five-minute
// timer needs nothing to go wrong.
func TestTickIsExclusive(t *testing.T) {
	cfg := testConfig(t)
	t.Setenv("E2E_ORCHESTRATOR_BIN", orchestrator(t))

	unlock, held, err := takeTickLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !held {
		t.Fatal("could not take the lock on a fresh root")
	}

	writeHook(t, cfg, eventRun, "50-work", `touch "$E2E_JOB_DIR/ran"`)
	if _, err := submit(cfg, mustRequest(t, "branch=main\nname=blocked\n")); err != nil {
		t.Fatal(err)
	}

	// A tick while the lock is held must not admit anything.
	if err := tick(cfg); err != nil {
		t.Errorf("a blocked tick reported an error: %v", err)
	}
	if left, _ := queued(cfg); len(left) != 1 {
		t.Errorf("a blocked tick consumed the queue: %d left, want 1", len(left))
	}
	live, _ := liveJobs(cfg)
	if len(live) != 0 {
		t.Errorf("a blocked tick started %d jobs", len(live))
	}

	// Released, the same tick does its work.
	unlock()
	if err := tick(cfg); err != nil {
		t.Fatal(err)
	}
	if got := waitForDone(t, cfg, "blocked"); got != "0" {
		t.Errorf("status: got %q, want 0", got)
	}
}

// TestAdmitSetsARejectedRequestAside checks what happens to a request which does
// not parse: it is renamed once, to a name queued ignores, and a second admit
// leaves it alone.
//
// Parses badly, and not "nobody can read it": the two are different cases and the
// difference now selects the branch. queuedKey runs first, before the cap, and
// reports no key for either, so both fall through to acceptRequest -- but only a
// request whose own contents are the problem comes back errBadRequest and is set
// aside. One which cannot be read at all may come right, and stays queued.
//
// The dot is the whole point. queued skips dotted names, so the rejected request is
// out of the listing for good; give it a plain suffix and admit picks it up again on
// the same pass, renames it again, and goes on lengthening the name until the kernel
// refuses it, which aborts the tick before anything is admitted and destroys the
// name on the way. Nothing else in the tree pins that dot, so this test is what
// stands between ignoredName and that regression.
func TestAdmitSetsARejectedRequestAside(t *testing.T) {
	cfg := testConfig(t)

	// A line with no "=" in it, so parseRequest refuses it and nothing about the
	// filesystem is at fault.
	if err := os.WriteFile(filepath.Join(cfg.queueDir(), "1-nonsense"),
		[]byte("this is not a field\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := admit(cfg); err != nil {
		t.Fatalf("admit gave up on a bad request: %v", err)
	}

	// Exactly one file, under the one name, and nothing left for queued to offer.
	want := ".1-nonsense.rejected"
	entries, err := os.ReadDir(cfg.queueDir())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != 1 || names[0] != want {
		t.Fatalf("queue holds %v, want just [%s]", names, want)
	}
	if left, _ := queued(cfg); len(left) != 0 {
		t.Fatalf("a rejected request is still on offer: %v", left)
	}

	// A second pass must not touch it.
	if err := admit(cfg); err != nil {
		t.Fatalf("a second admit failed: %v", err)
	}
	entries, err = os.ReadDir(cfg.queueDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != want {
		t.Errorf("a second admit renamed it again: %v", entries)
	}
}

// TestAdmitStepsOverAnUnacceptableRequest checks that a request which cannot be
// accepted this tick does not block the ones behind it: with the cap at one, a queue
// holding an unsatisfiable entry followed by a good one starts the good one in that
// same tick.
//
// The unsatisfiable entry names a file which is not there, which is the case that
// forced this: it parses, so it is not set aside, and it may come right, so it is not
// discarded. Retrying it from the front of the queue instead of stepping over it
// meant one such request stalled all admission on every tick for good.
//
// The entry is left queued on purpose. Nothing here asserts it will ever succeed;
// what is asserted is that waiting is what it does, rather than taking the rest of
// the queue down with it.
func TestAdmitStepsOverAnUnacceptableRequest(t *testing.T) {
	cfg := testConfig(t)
	t.Setenv("E2E_ORCHESTRATOR_BIN", orchestrator(t))

	writeHook(t, cfg, eventRun, "50-work", `true`)

	// Explicit names, because the queue is ordered by name and this test is about
	// the bad one coming first.
	blocked := filepath.Join(cfg.queueDir(), "1-unsatisfiable")
	if err := os.WriteFile(blocked,
		[]byte("branch=main\nname=waits\nworktree_FILE=/nowhere/at/all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.queueDir(), "2-good"),
		[]byte("branch=other\nname=goes-anyway\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := tick(cfg); err != nil {
		t.Fatalf("an unacceptable request ended the tick: %v", err)
	}

	if got := waitForDone(t, cfg, "goes-anyway"); got != "0" {
		t.Errorf("status of the job behind the unacceptable one: got %q, want 0", got)
	}
	// Still queued, neither set aside nor thrown away.
	if _, err := os.Stat(blocked); err != nil {
		t.Errorf("the unsatisfiable request did not stay queued: %v", err)
	}
}

// TestSweepCarriesOnPastAFinishFailure checks that one job which cannot be disposed
// of does not stop the tick, by giving a job a done/ name which is already taken so
// its finish can never succeed.
//
// Before this, finish returning an error ended the sweep, so tick returned before
// admit and one stuck job blocked all admission for as long as it sat there. The
// collision is permanent, so "the next tick will fix it" was not true either.
func TestSweepCarriesOnPastAFinishFailure(t *testing.T) {
	cfg := testConfig(t)
	t.Setenv("E2E_ORCHESTRATOR_BIN", orchestrator(t))

	writeHook(t, cfg, eventRun, "50-work", `true`)

	// A job whose work had started, abandoned by a pid which is not running.
	path, _ := submit(cfg, mustRequest(t, "branch=main\nname=stuck\n"))
	stuck, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := stuck.markStarted(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stuck.Dir, jobPid), []byte("999999 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// done/stuck already taken, and not empty, so renaming onto it is ENOTEMPTY
	// however often it is tried.
	blocking := filepath.Join(cfg.doneDir(), "stuck")
	if err := os.MkdirAll(blocking, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocking, jobStatus), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A second request, which the same tick must still get to.
	if _, err := submit(cfg, mustRequest(t, "branch=other\nname=runs-anyway\n")); err != nil {
		t.Fatal(err)
	}

	if err := tick(cfg); err != nil {
		t.Fatalf("a job which cannot be finished ended the tick: %v", err)
	}

	if got := waitForDone(t, cfg, "runs-anyway"); got != "0" {
		t.Errorf("status of the job admitted anyway: got %q, want 0", got)
	}
	// And the stuck one is still there for a later tick, not silently lost.
	if _, err := os.Stat(stuck.Dir); err != nil {
		t.Errorf("the stuck job went missing: %v", err)
	}
}

// TestRunJobFailsBeforeMarkingStarted checks that a supervisor which cannot read its
// job gives up while the job still counts as unstarted.
//
// Order is the whole of it. Failing after markStarted would leave a job no sweep can
// ever requeue and no process is running, so it would hold its place until somebody
// noticed; failing before it leaves the sweep free to dispose of the job.
func TestRunJobFailsBeforeMarkingStarted(t *testing.T) {
	cfg := testConfig(t)

	// An empty request: present, so this is not the missing-file case, and empty, so
	// nothing in it describes a job.
	dir := filepath.Join(cfg.jobsDir(), "unreadable")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, jobRequest), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if status := runJob(cfg, "unreadable"); status == 0 {
		t.Error("a job with no readable request was supervised as a success")
	}
	if _, err := os.Stat(filepath.Join(dir, jobStarted)); !os.IsNotExist(err) {
		t.Error("the work was marked as started despite the job being unreadable")
	}
}

// TestRunJobFailsWithNoRunHook checks that a job with nothing to run fails rather
// than being recorded as a success.
//
// This is the one failure shape a nightly must not have. With 50-e2e-tests left
// unexecutable, the job used to reach done/ with a status of 0 and the only sign of
// it was one line in the job's own log, which nobody reads after a green run; and
// the poll hook had already written down that it asked, so nothing would ask again
// for a day. A whole day of PASS having tested nothing.
func TestRunJobFailsWithNoRunHook(t *testing.T) {
	cfg := testConfig(t)

	// A run directory holding one file which is not a hook, for want of an execute
	// bit. The measured case, and not merely a missing directory.
	dir := filepath.Join(cfg.Hooks, eventRun)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "50-e2e-tests"),
		[]byte("#!/bin/bash\ntrue\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, err := submit(cfg, mustRequest(t, "branch=main\nname=no-work\n"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	if status := runJob(cfg, job.ID); status == 0 {
		t.Error("a job with no usable run hook was supervised as a success")
	}
	if got := strings.TrimSpace(string(mustReadFile(t,
		filepath.Join(cfg.doneDir(), job.ID, jobStatus)))); got == "0" {
		t.Errorf("done/%s/status is %q, want non-zero", job.ID, got)
	}
	// Failed before the work could be said to have begun, so nothing has published
	// anything and the record is honest about that.
	if _, err := os.Stat(filepath.Join(cfg.doneDir(), job.ID, jobStarted)); err == nil {
		t.Error("the work was marked as started with no run hook to do it")
	}
}

// TestAdmitDropsARequestWhoseKeyIsRunning checks that the two documented ways of
// asking for work deduplicate alike. submit refuses a key which is already active,
// but a request written into $E2E_QUEUE and renamed in never goes through submit,
// and that path is the whole reason the queue is a directory. Before this, two
// requests carrying the same key, renamed in, both became jobs, so a poll hook
// dropping a file on every tick started the same work on top of itself.
//
// Renamed in rather than submitted on purpose: going through submit would be
// refused there and would pin nothing about admit.
func TestAdmitDropsARequestWhoseKeyIsRunning(t *testing.T) {
	cfg := testConfig(t)
	// Room for two, so that whatever drops the second request it is not the cap.
	cfg.MaxJobs = 2
	// A startJob which does nothing, so a duplicate wrongly accepted still leaves
	// its job directory behind for the assertion to find, and no supervisor runs.
	t.Setenv("E2E_ORCHESTRATOR_BIN", "/bin/true")

	// A job holding the key, made live by a real process: alive() compares the
	// recorded start time against /proc, so an invented pid would not do.
	path, err := submit(cfg, mustRequest(t, "key=same@main\nbranch=main\nname=running\n"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	sleep := exec.Command("sleep", "60")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sleep.Process.Kill()
		_ = sleep.Wait()
	}()
	if err := job.recordPid(sleep.Process.Pid); err != nil {
		t.Fatal(err)
	}

	// The second request, written and renamed in, which is the path submit's own
	// check never sees.
	duplicate := filepath.Join(cfg.queueDir(), "9-duplicate")
	if err := os.WriteFile(filepath.Join(cfg.queueDir(), ".9-duplicate"),
		[]byte("key=same@main\nbranch=main\nname=second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(cfg.queueDir(), ".9-duplicate"), duplicate); err != nil {
		t.Fatal(err)
	}

	if err := admit(cfg); err != nil {
		t.Fatal(err)
	}

	jobs, err := allJobs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		var names []string
		for _, job := range jobs {
			names = append(names, job.ID)
		}
		t.Errorf("jobs are %v, want just the one already running", names)
	}
	// Gone, not merely passed over: a redundant request left queued would be
	// reconsidered on every tick and started once the job it duplicates ended.
	if _, err := os.Stat(duplicate); !os.IsNotExist(err) {
		t.Errorf("the redundant request was not removed: %v", err)
	}
	if left, _ := queued(cfg); len(left) != 0 {
		t.Errorf("%d requests left queued, want 0", len(left))
	}
}

// TestAdmitDropsARedundantRequestWithNoRoomToStartIt checks the same drop at a cap
// of one, which is the cap every host ships with and the case the check exists for.
//
// The order of the two is the whole of it. With the cap tested first, admit returned
// the moment the slot was taken and the drop was unreachable for exactly the run
// which makes a request redundant: a branch moving during a run left a request the
// cap stepped away from, and the tick after the run started it on a tip already
// tested. Measured as three further runs for three merges during one run.
func TestAdmitDropsARedundantRequestWithNoRoomToStartIt(t *testing.T) {
	cfg := testConfig(t)
	// The shipped cap, and no room at all once the job below is live.
	cfg.MaxJobs = 1
	t.Setenv("E2E_ORCHESTRATOR_BIN", "/bin/true")

	path, err := submit(cfg, mustRequest(t, "key=same@main\nbranch=main\nname=running\n"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	// A real process, because alive() corroborates the pid against the start time it
	// was recorded with and an invented one would read as dead.
	sleep := exec.Command("sleep", "60")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sleep.Process.Kill()
		_ = sleep.Wait()
	}()
	if err := job.recordPid(sleep.Process.Pid); err != nil {
		t.Fatal(err)
	}

	duplicate := filepath.Join(cfg.queueDir(), "9-duplicate")
	if err := os.WriteFile(duplicate,
		[]byte("key=same@main\nbranch=main\nname=second\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := admit(cfg); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(duplicate); !os.IsNotExist(err) {
		t.Errorf("a redundant request survived a full cap: %v", err)
	}
	if left, _ := queued(cfg); len(left) != 0 {
		t.Errorf("%d requests left queued, want 0", len(left))
	}
}

// TestTickRunsTickHooksFirst checks that a request a tick hook submits is
// considered by that same tick, so polling does not wait five minutes to act on
// what it just found.
func TestTickRunsTickHooksFirst(t *testing.T) {
	cfg := testConfig(t)
	t.Setenv("E2E_ORCHESTRATOR_BIN", orchestrator(t))

	writeHook(t, cfg, eventTick, "10-ask", `
printf 'branch=main\nname=asked-for\n' > "$E2E_QUEUE/.new" && mv "$E2E_QUEUE/.new" "$E2E_QUEUE/new"`)
	writeHook(t, cfg, eventRun, "50-work", `true`)

	if err := tick(cfg); err != nil {
		t.Fatal(err)
	}
	if got := waitForDone(t, cfg, "asked-for"); got != "0" {
		t.Errorf("status: got %q", got)
	}
}
