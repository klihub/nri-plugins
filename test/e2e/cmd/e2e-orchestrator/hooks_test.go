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
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// writeHook puts an executable hook in place for an event.
func writeHook(t *testing.T, cfg *Config, event, name, body string) {
	t.Helper()

	dir := filepath.Join(cfg.Hooks, event)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/bash\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestHooksRunInLexicalOrder checks the order hooks run in, which is the only
// thing a hook author has to reason about when one must follow another.
func TestHooksRunInLexicalOrder(t *testing.T) {
	cfg := testConfig(t)
	writeHook(t, cfg, eventTick, "20-second", `echo second`)
	writeHook(t, cfg, eventTick, "10-first", `echo first`)
	writeHook(t, cfg, eventTick, "90-third", `echo third`)

	var out bytes.Buffer
	status, err := runHooks(cfg, eventTick, nil, nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if status != 0 {
		t.Errorf("status: got %d, want 0", status)
	}
	if got := strings.Fields(out.String()); strings.Join(got, ",") != "first,second,third" {
		t.Errorf("order: got %v", got)
	}
}

// TestHooksIgnoreDottedAndUnexecutableNames checks that an editor's leftover does
// not become policy.
//
// The `~` case is the one that matters and the reason ignoredName exists: emacs
// keeps the mode of a backup, so editing a hook leaves an executable copy of it in
// the same directory. Running both would mean superseded logic acting as policy,
// which for the polling hook means duplicate requests. A hook is conventionally
// named without a dot, so skipping dotted names alone would not catch it.
func TestHooksIgnoreDottedAndUnexecutableNames(t *testing.T) {
	cfg := testConfig(t)
	writeHook(t, cfg, eventTick, "10-real", `echo real`)
	writeHook(t, cfg, eventTick, "20-backup.bak", `echo backup`)
	writeHook(t, cfg, eventTick, ".30-hidden", `echo hidden`)
	writeHook(t, cfg, eventTick, "10-real~", `echo emacs-backup`)
	writeHook(t, cfg, eventTick, "#10-real#", `echo emacs-autosave`)

	plain := filepath.Join(cfg.Hooks, eventTick, "40-not-executable")
	if err := os.WriteFile(plain, []byte("#!/bin/bash\necho plain\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if _, err := runHooks(cfg, eventTick, nil, nil, &out); err != nil {
		t.Fatal(err)
	}
	// Filter out the warning lines about ignored hooks.
	var hookOutput []string
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.Contains(line, "ignoring") {
			hookOutput = append(hookOutput, line)
		}
	}
	if got := strings.TrimSpace(strings.Join(hookOutput, "\n")); got != "real" {
		t.Errorf("got %q, want real", got)
	}
}

// TestHooksStopAtAVeto checks that a non-zero pre-run hook cancels, and that no
// later hook runs. A veto is a decision, so nothing after it should get a say.
func TestHooksStopAtAVeto(t *testing.T) {
	cfg := testConfig(t)
	writeHook(t, cfg, eventPreRun, "10-yes", `echo yes`)
	writeHook(t, cfg, eventPreRun, "20-no", `echo no; exit 7`)
	writeHook(t, cfg, eventPreRun, "30-never", `echo never`)

	var out bytes.Buffer
	status, err := runHooks(cfg, eventPreRun, nil, nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if status != 7 {
		t.Errorf("status: got %d, want 7", status)
	}
	if strings.Contains(out.String(), "never") {
		t.Error("a hook after the veto ran")
	}
}

// TestHooksPassTheEnvironment checks that a hook is told everything, with the
// encoding suffixes left intact so it knows what it is holding.
func TestHooksPassTheEnvironment(t *testing.T) {
	cfg := testConfig(t)
	writeHook(t, cfg, eventPostRun, "10-say", `
echo "event=$E2E_EVENT job=$E2E_JOB branch=$E2E_BRANCH"
echo "json=$E2E_PRS_JSON status=$E2E_STATUS"
echo "queue=$E2E_QUEUE hooks=$E2E_HOOKS state=$E2E_STATE"`)

	path, err := submit(cfg, mustRequest(t, `branch=main`+"\n"+`prs_JSON=[1,2]`+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if _, err := runHooks(cfg, eventPostRun, job, map[string]string{"E2E_STATUS": "0"}, &out); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"event=post-run", "job=" + job.ID, "branch=main",
		"json=[1,2]", "status=0",
		"queue=" + cfg.queueDir(), "hooks=" + cfg.Hooks, "state=" + cfg.stateDir(),
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

// TestHooksTimeOut checks that a hook which will not finish is killed, and that
// its process group goes with it so a child it left cannot outlive it.
func TestHooksTimeOut(t *testing.T) {
	cfg := testConfig(t)
	cfg.HookTimeout = 300 * time.Millisecond
	pidfile := filepath.Join(cfg.Root, "child.pid")
	writeHook(t, cfg, eventTick, "10-forever", `
sh -c 'echo $$ > `+pidfile+`; sleep 60' &
sleep 60
`)

	var out bytes.Buffer
	start := time.Now()
	status, err := runHooks(cfg, eventTick, nil, nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if status == 0 {
		t.Error("a hook which timed out reported success")
	}
	elapsed := time.Since(start)
	// Two seconds says SIGTERM ended it. A looser bound would pass for the whole
	// SIGKILL escalation too, which is the thing worth ruling out.
	if elapsed > 2*time.Second {
		t.Errorf("waited %v for a hook with a 300ms timeout", elapsed)
	}
	// One line, and the true one: the hook did not exit, it was cut off.
	if !strings.Contains(out.String(), "timed out after") {
		t.Errorf("no timeout reported, out is:\n%s", out.String())
	}
	if strings.Contains(out.String(), "exited") {
		t.Errorf("a hook that timed out was also reported as exited:\n%s", out.String())
	}

	// And the grandchild went with the group. Signal 0 asks whether a process is
	// there without touching it. os.Signal(nil) cannot be used for this: Process
	// .Signal rejects it before it ever reaches the kernel, so it fails for a live
	// process exactly as for a dead one and the question is never asked.
	//
	// Asked repeatedly rather than once, because the grandchild's parent is the
	// hook we just killed, so for a few milliseconds the grandchild is a zombie
	// reparented to init, and signal 0 to a zombie succeeds. One which really
	// escaped the group is still there when the deadline runs out.
	data, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatalf("no pid for the grandchild, so nothing was checked: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("grandchild pid %q: %v", data, err)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for p.Signal(syscall.Signal(0)) == nil {
		if time.Now().After(deadline) {
			t.Errorf("grandchild %d still running after the timeout", pid)
			_ = p.Signal(syscall.SIGKILL)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRunHookHasNoTimeout checks that run is not bounded. Work taking hours is the
// normal case, and a scheduler guessing how long is worse than not guessing.
func TestRunHookHasNoTimeout(t *testing.T) {
	cfg := testConfig(t)
	cfg.HookTimeout = 100 * time.Millisecond
	writeHook(t, cfg, eventRun, "50-work", `sleep 0.5; echo finished`)

	var out bytes.Buffer
	status, err := runHooks(cfg, eventRun, nil, nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if status != 0 {
		t.Errorf("status: got %d, want 0", status)
	}
	if !strings.Contains(out.String(), "finished") {
		t.Error("the run hook was cut short")
	}
}

// TestNoHooksIsNotAFailure checks that an event nobody reacts to is fine. Most
// events have no hooks on most hosts.
func TestNoHooksIsNotAFailure(t *testing.T) {
	cfg := testConfig(t)

	status, err := runHooks(cfg, eventPostRun, nil, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if status != 0 {
		t.Errorf("status: got %d, want 0", status)
	}
}

// TestTickContinuesAfterFailureButRunStops checks the key difference between tick
// and run in their handling of non-zero exits. Tick logs and continues; run stops
// with the failure as the job outcome.
func TestTickContinuesAfterFailureButRunStops(t *testing.T) {
	cfg := testConfig(t)

	// Tick event continues after a failure.
	writeHook(t, cfg, eventTick, "10-fail", `echo fail; exit 5`)
	writeHook(t, cfg, eventTick, "20-after", `echo after`)
	var out bytes.Buffer
	status, err := runHooks(cfg, eventTick, nil, nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if status != 5 {
		t.Errorf("tick status: got %d, want 5 (first failure)", status)
	}
	if !strings.Contains(out.String(), "after") {
		t.Errorf("tick did not run the hook after a failure")
	}

	// Run event stops at a failure.
	cfg2 := testConfig(t)
	writeHook(t, cfg2, eventRun, "10-fail", `echo fail; exit 3`)
	writeHook(t, cfg2, eventRun, "20-after", `echo after`)
	var out2 bytes.Buffer
	status, err = runHooks(cfg2, eventRun, nil, nil, &out2)
	if err != nil {
		t.Fatal(err)
	}
	if status != 3 {
		t.Errorf("run status: got %d, want 3", status)
	}
	if strings.Contains(out2.String(), "after") {
		t.Errorf("run continued after a failure")
	}
}

// TestRequestFieldDoesNotShadowOrchestratorVariable checks that the orchestrator
// variables override request field values on a name collision.
//
// The branch is asserted as well as the log, and that is not padding: a hookEnv
// which stopped passing request fields altogether would satisfy the precedence
// claim perfectly. What is wanted is both halves -- fields survive, and ours win.
func TestRequestFieldDoesNotShadowOrchestratorVariable(t *testing.T) {
	cfg := testConfig(t)
	writeHook(t, cfg, eventPostRun, "10-say", `
echo "log=$E2E_LOG"
echo "branch=$E2E_BRANCH"`)

	path, err := submit(cfg, mustRequest(t, `branch=main
log=request-value
`))
	if err != nil {
		t.Fatal(err)
	}
	job, err := acceptRequest(cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if _, err := runHooks(cfg, eventPostRun, job, nil, &out); err != nil {
		t.Fatal(err)
	}

	want := "log=" + filepath.Join(job.Dir, jobLog)
	if !strings.Contains(out.String(), want) {
		t.Errorf("E2E_LOG got overridden by request field; wanted %q in:\n%s", want, out.String())
	}
	if !strings.Contains(out.String(), "branch=main") {
		t.Errorf("a request field did not reach the hook at all; out is:\n%s", out.String())
	}
}

// TestEveryHookIsToldWhereTheOrchestratorIs checks that E2E_ORCHESTRATOR reaches a
// tick hook and not only a job's.
//
// A tick hook is the case that matters: it is where polling lives, and with no way
// to invoke us it can only submit by writing a file into the queue and renaming it
// in. That path is compared against running work alone, so it cannot tell that the
// same key is already waiting, which is how a branch moving twice during a run came
// to queue two requests for the same tip.
func TestEveryHookIsToldWhereTheOrchestratorIs(t *testing.T) {
	cfg := testConfig(t)
	// Set to something wrong on purpose: hookEnv appends its own values after the
	// environment it inherits, so ours has to be what the hook sees.
	t.Setenv("E2E_ORCHESTRATOR", "/nowhere/at/all")
	writeHook(t, cfg, eventTick, "10-say", `echo "orchestrator=$E2E_ORCHESTRATOR"`)

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if _, err := runHooks(cfg, eventTick, nil, nil, &out); err != nil {
		t.Fatal(err)
	}

	want := "orchestrator=" + self
	if !strings.Contains(out.String(), want) {
		t.Errorf("wanted %q in:\n%s", want, out.String())
	}
}
