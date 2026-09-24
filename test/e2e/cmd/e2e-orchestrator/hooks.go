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
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// The events. Four, with detail in the environment rather than in more names.
const (
	// eventTick is the clock, before anything is admitted. Hooks may submit.
	eventTick = "tick"
	// eventPreRun is a job holding a slot with no work begun. Vetoable.
	eventPreRun = "pre-run"
	// eventRun performs the work. Unbounded in time.
	eventRun = "run"
	// eventPostRun is the work having finished, with its status.
	eventPostRun = "post-run"
)

// hooksFor is the hooks of an event, in the order they run.
//
// Lexical order, which is why they are named with two digits. Anything with a dot
// in its name is skipped so that an editor's backup or a disabled copy does not
// silently become policy, and so is anything not executable, and so is anything
// that matches ignoredName: the editor artifacts which carry no dot at all.
func hooksFor(cfg *Config, event string, out io.Writer) ([]string, error) {
	dir := filepath.Join(cfg.Hooks, event)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, entry := range entries {
		name := entry.Name()
		// A dot rules out a backup or a disabled copy with a suffix; ignoredName
		// rules out the editor artifacts which carry no dot at all. Emacs keeps
		// the mode of a backup, so an executable 10-poll-branch~ beside the hook
		// would otherwise run as policy in its own right.
		if entry.IsDir() || strings.Contains(name, ".") || ignoredName(name) {
			if !entry.IsDir() && !strings.HasPrefix(name, ".") {
				fmt.Fprintf(out, "%s: ignoring %s\n", event, name)
			}
			continue
		}
		info, err := entry.Info()
		if err != nil {
			fmt.Fprintf(out, "%s: ignoring %s, cannot stat it: %v\n", event, name, err)
			continue
		}
		if info.Mode()&0o111 == 0 {
			fmt.Fprintf(out, "%s: ignoring %s, not executable\n", event, name)
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(dir, name))
	}

	return paths, nil
}

// hookEnv is the environment a hook is given.
//
// Every request field arrives prefixed and upper-cased with its encoding suffix
// intact, so a hook can tell a scalar from JSON from a file by the name alone.
// E2E_ORCHESTRATOR is the path to this binary, so a hook submits with
// "$E2E_ORCHESTRATOR" request <key=value ...> and emits with
// "$E2E_ORCHESTRATOR" emit <event> [key=value ...]. The path alone rather than a
// ready-made command, so that a hook can quote it like every other variable.
func hookEnv(cfg *Config, event string, job *Job, extra map[string]string) []string {
	env := os.Environ()
	add := func(name, value string) {
		env = append(env, name+"="+value)
	}

	// The request's own fields first, so that the orchestrator's values below win
	// on a name collision. The last duplicate is what execve hands the process, and
	// this ordering is relied upon: a request field called "log" must not be able
	// to redirect E2E_LOG.
	if job != nil {
		for _, field := range job.Request.Fields {
			add("E2E_"+strings.ToUpper(field.Name), field.Value)
		}
	}

	add("E2E_EVENT", event)
	add("E2E_HOOKS", cfg.Hooks)
	add("E2E_QUEUE", cfg.queueDir())
	add("E2E_STATE", cfg.stateDir())
	// For every hook and not only a job's. A tick hook which has no way to invoke
	// us has no way to submit but to write a file into the queue and rename it in,
	// and that path is only ever compared against running work: submit's check
	// against what is already queued is the one that makes a branch moving twice
	// during a run into one request rather than two.
	//
	// Added here, after the request's fields, so that this wins over anything an
	// operator exported under the same name -- e2e-cron-job exports it as the binary
	// to invoke, which is the same value said by a different party.
	if self, err := os.Executable(); err == nil {
		add("E2E_ORCHESTRATOR", self)
	} else {
		// Said out loud rather than passed over, so that the contract can promise
		// this to every hook without hedging. A hook whose whole job is to ask for
		// work has no other way in and will refuse; leaving the variable quietly
		// unset would have the hook complain and the tick look blameless.
		warnf("cannot tell where I am, so %s hooks get no E2E_ORCHESTRATOR: %v",
			event, err)
	}

	if job != nil {
		add("E2E_JOB", job.ID)
		add("E2E_JOB_DIR", job.Dir)
		add("E2E_LOG", filepath.Join(job.Dir, jobLog))
	}

	for name, value := range extra {
		add(name, value)
	}

	return env
}

// runHooks runs the hooks of an event and says how it went.
//
// For pre-run, a non-zero exit is a veto and stops the sequence. For run, it is
// the job's outcome and stops the sequence. For tick and post-run, it is advisory:
// the hooks continue, and runHooks reports the first failure seen (or 0 if all pass).
func runHooks(cfg *Config, event string, job *Job, extra map[string]string, out io.Writer) (int, error) {
	hooks, err := hooksFor(cfg, event, out)
	if err != nil {
		return 0, err
	}
	// Nothing to run, so nothing to prepare for. Most events have no hooks on most
	// hosts, and making a state directory for them was the one side effect this had
	// on an event which does nothing.
	if len(hooks) == 0 {
		return 0, nil
	}
	// state/ only, and that is the whole of what this call keeps. A hook is promised
	// both this and the queue directory, but by different things: tick makes the pair
	// before its own hooks run, so anything a tick dispatches has both, while this is
	// what keeps state/ for the events a supervisor dispatches -- its own process, and
	// possibly the first thing on a fresh root to need the directory. Those events
	// have a queue directory because the tick which admitted the job made it, which is
	// transitive rather than guaranteed, and doc.go says exactly that rather than more.
	if err := os.MkdirAll(cfg.stateDir(), 0o755); err != nil {
		return 0, err
	}

	env := hookEnv(cfg, event, job, extra)
	first := 0

	for _, hook := range hooks {
		status, note := runOneHook(cfg, event, hook, env, childOut{w: out})
		if status == 0 {
			continue
		}

		// Said here and not in runOneHook, because while a hook runs os/exec's
		// copy goroutine is writing the hook's own output to out, and out takes
		// one writer at a time. A bytes.Buffer is the case that bites: its
		// ReadFrom restores the length it saw before the read it is parked in, so
		// a line written beside it disappears without a trace. By here cmd.Wait
		// has returned, the copy is over, and out is ours alone.
		if note == "" {
			note = fmt.Sprintf("%s exited %d", filepath.Base(hook), status)
		}
		fmt.Fprintf(out, "%s: %s\n", event, note)

		// A veto stops the sequence, and so does failed work: for those two the
		// status is a decision about the job. Elsewhere it is advice, so the rest
		// of the hooks still get their turn and the first failure is what we
		// report.
		if event == eventPreRun || event == eventRun {
			return status, nil
		}
		if first == 0 {
			first = status
		}
	}

	return first, nil
}

// childOut carries the writer a hook's own output goes to, without being a writer
// itself.
//
// Deliberate. Anything written to that writer between Start and Wait is discarded
// without a trace, because os/exec's copy goroutine runs bytes.Buffer.ReadFrom,
// which reslices away whatever another writer appended while it was parked in Read.
// Keeping the writer out of runOneHook's scope makes that a compile error rather
// than a convention: runOneHook reports through its note return and writes nothing.
type childOut struct {
	w io.Writer
}

// attach hands a command the hook's output writer.
func (c childOut) attach(cmd *exec.Cmd) {
	// Both streams get the same value, so os/exec's interfaceEqual path gives them
	// one pipe and one copy goroutine.
	cmd.Stdout = c.w
	cmd.Stderr = c.w
}

// runOneHook runs a single hook, bounded in time unless it is the work itself.
// It returns the status and a note for the caller to report in place of the plain
// "exited" line, the note being empty when the hook merely exited.
//
// It writes nothing itself; it cannot, having only a childOut. From Start until
// Wait returns that writer belongs to the copy goroutine os/exec runs for the
// hook's own output, and a second writer there loses its line: see childOut.
func runOneHook(cfg *Config, event, hook string, env []string, out childOut) (int, string) {
	ctx := context.Background()
	if event != eventRun {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.HookTimeout)
		defer cancel()
	}

	cmd := exec.Command(hook)
	cmd.Env = env
	out.attach(cmd)
	// Its own process group so signalGroup can kill the whole tree. A hook which
	// leaves a child behind with setsid escapes the group, but that is a fault of
	// the hook, not of us.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Bound the wait for output-copy goroutines. When the hook child exits but a
	// descendant holds the output pipe open, this timeout ends the wait without
	// waiting forever.
	cmd.WaitDelay = 10 * time.Second

	if err := cmd.Start(); err != nil {
		warnf("%s: %s will not run: %v", event, filepath.Base(hook), err)
		return 1, fmt.Sprintf("%s will not run: %v", filepath.Base(hook), err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return exitStatus(err), ""
	case <-ctx.Done():
		signalGroup(cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			// SIGTERM did not end the hook; escalate to SIGKILL. This happens
			// within the WaitDelay window so cmd.Wait does not hang.
			signalGroup(cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}

		return 1, fmt.Sprintf("%s timed out after %v", filepath.Base(hook), cfg.HookTimeout)
	}
}

// exitStatus turns a Wait error into a status, anything unrecognised being a
// failure rather than a success. A process killed by a signal returns 128 + signal.
func exitStatus(err error) int {
	if err == nil {
		return 0
	}

	if errors.Is(err, exec.ErrWaitDelay) {
		// The hook itself finished; something it left behind held the output open.
		// Wait prefers a real ExitError over this, so a genuine non-zero is never
		// masked here.
		return 0
	}

	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code := exit.ExitCode()
		if code == -1 {
			// Killed by a signal. Exit code -1 means the process was terminated
			// by a signal, not exited. Extract the signal and use 128 + signal.
			if ws, ok := exit.Sys().(syscall.WaitStatus); ok {
				if sig := ws.Signal(); sig != 0 {
					return 128 + int(sig)
				}
			}
			return 1
		}
		return code
	}

	return 1
}
