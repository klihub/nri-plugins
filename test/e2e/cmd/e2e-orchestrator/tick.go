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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// tickLock is the name, under E2E_ROOT, of the lock which makes a tick exclusive.
// Dotted so it is not mistaken for anything the orchestrator keeps.
const tickLock = ".tick.lock"

// tick does what is due and returns. It never waits for work.
//
// In order: let hooks ask for things, tidy up after whatever died, then start what
// there is room for. Asking comes first so that a poll which finds something new
// acts on it in the same tick rather than five minutes later.
func tick(cfg *Config) error {
	// One tick at a time. Not the lock this project removes -- that one was held
	// for the hours a run takes, by the thing doing the work. This one is held for
	// the seconds a decision takes, and without it the cap means nothing: two
	// overlapping ticks each count the live jobs, each see room, and each admit.
	// A tick hook may run for E2E_HOOK_TIMEOUT, so overlapping a five-minute timer
	// needs nothing to go wrong.
	unlock, held, err := takeTickLock(cfg)
	if err != nil {
		return err
	}
	if !held {
		fmt.Println("another tick is deciding, nothing to do")
		return nil
	}
	defer unlock()

	// The two directories a hook is promised before it runs: its own state, and the
	// queue it submits to. Made here rather than by whoever first needs one, so that
	// a poll hook does not have to make the queue before it can ask for anything --
	// which is a thing to get wrong once per hook, on a fresh root, silently.
	for _, dir := range []string{cfg.queueDir(), cfg.stateDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	// A warning and not a return, for the reason sweep gives one job's trouble: a
	// hooks.d/tick which is a file rather than a directory, or which cannot be read,
	// must not stop the sweeping and the admitting while the queue fills up. What
	// the hooks themselves exit with is already advice, handled in runHooks; this is
	// the dispatch failing.
	if _, err := runHooks(cfg, eventTick, nil, nil, os.Stdout); err != nil {
		warnf("tick hooks: %v", err)
	}
	if err := sweep(cfg); err != nil {
		return err
	}

	// Said here because nothing a job says can reach the cron log. Its supervisor is
	// detached with the job log as both its streams, and a job which fails for want
	// of a run hook publishes nothing, so done/<job>/status is otherwise the only
	// trace of it -- there is not even a results page to be empty. This is the line
	// somebody finds when the nightly has gone quiet.
	//
	// Unconditional, and not only when there is something to admit: a deployment with
	// no run hook is misconfigured whether or not anything has asked for work yet,
	// and hearing so before the first request is better than after it.
	if work, err := hooksFor(cfg, eventRun, os.Stdout); err != nil {
		warnf("%s hooks: %v", eventRun, err)
	} else if len(work) == 0 {
		warnf("no %s hook in %s, so anything admitted has no work to do",
			eventRun, filepath.Join(cfg.Hooks, eventRun))
	}

	return admit(cfg)
}

// takeTickLock takes the lock which makes one tick exclusive.
//
// Held on a descriptor with flock, so the kernel drops it however the tick ends
// and there is no stale lock to clear after a crash. Not held means another tick
// is deciding, which is not an error: the next timer will come round.
func takeTickLock(cfg *Config) (func(), bool, error) {
	if err := os.MkdirAll(cfg.Root, 0o755); err != nil {
		return nil, false, err
	}

	file, err := os.OpenFile(filepath.Join(cfg.Root, tickLock),
		os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}

	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}

	return func() { _ = file.Close() }, true, nil
}

// sweep accounts for jobs whose supervisor is gone.
//
// A job which never began its work is safe to queue again: nothing has happened
// that anybody could be reading. One whose work had started is not, because it may
// have published part of something and we cannot know what, so it is recorded as
// abandoned and left alone.
//
// One job's trouble must never end the sweep. A collision in done/ makes finish
// fail for good, and a job directory which goes away between the listing and the
// call fails with ENOENT; either one returning here would mean tick never reaches
// admit, so a single stuck job would block all admission forever. Say so, carry
// on, and let the next tick try again.
func sweep(cfg *Config) error {
	jobs, err := allJobs(cfg)
	if err != nil {
		return err
	}

	for _, job := range jobs {
		if job.alive() {
			continue
		}
		if job.started() {
			if err := job.finish(cfg, 1); err != nil {
				warnf("%s: %v", job.ID, err)
				continue
			}
			// In the job's own log as well as on our output, because the two are read
			// at different times by different people. done/<id>/log is what an operator
			// is pointed at afterwards, and a job whose supervisor was killed mid-run
			// leaves a log which simply stops: without this the reason is only in the
			// tick's output, which is a file of the crontab line's choosing and is
			// rotated or discarded on its own schedule.
			//
			// After finish, not before it, for two reasons. finish moves the directory
			// and sets j.Dir, so this lands in done/ where it will be read rather than
			// in a jobs/ entry which is about to move. And a finish which keeps failing
			// is retried on every tick, so saying it first would add a line per tick to
			// a job nobody can dispose of.
			if log, err := openJobLog(job); err != nil {
				warnf("%s: %v", job.ID, err)
			} else {
				fmt.Fprintf(log, "sweep: supervisor gone and the work had begun,"+
					" recorded as abandoned\n")
				_ = log.Close()
			}
			fmt.Printf("%s was abandoned, not starting it again\n", job.ID)
			continue
		}
		if err := job.requeue(cfg); err != nil {
			// Refused, so this one is not to be tried again. Record it and stop.
			warnf("%s: %v", job.ID, err)
			if err := job.finish(cfg, 1); err != nil {
				warnf("%s: %v", job.ID, err)
				continue
			}
			// Why it was refused, said accurately: a job whose request never read
			// back was never tried at all, so calling that a second failure would
			// be untrue and would send whoever reads the log looking for a first.
			if len(job.Request.Fields) == 0 {
				fmt.Printf("%s has no readable request, giving up on it\n", job.ID)
			} else {
				fmt.Printf("%s died before starting twice, giving up on it\n", job.ID)
			}
		}
	}

	return nil
}

// admit starts what there is room for, oldest request first.
//
// The queue is listed once and walked, rather than re-listed and retried from the
// front after each start, so that an entry which cannot be accepted this tick is
// stepped over instead of blocking everything behind it. One request with a file
// field naming something that never appears would otherwise stall all admission on
// every tick, for good, which is the same permanent stall the sweep is careful to
// avoid one function up.
//
// A request whose own contents are the problem is set aside for good; anything else
// is left where it is for a later tick, because the reason may pass and discarding
// work somebody asked for is worse than making it wait.
//
// Termination is the length of the list: every entry is considered at most once.
//
// What listing once costs: a request which arrives during the walk waits for the
// next tick. A request submitted by a tick hook is not affected, since those hooks
// have finished by the time this is called, which is what the brief wanted and what
// TestTickRunsTickHooksFirst pins. What waits is a late arrival -- a hook of a job
// started moments ago, or a producer landing mid-walk -- and that is a latency of
// one interval, not a request lost.
//
// Dedup by key happens here and not only in submit, because a request renamed into
// the queue never goes through submit at all, and that is a documented way to ask
// for work -- the whole reason the queue is a directory. Without this check the two
// ways of submitting would not be equivalent, and a poll hook dropping a file on
// every tick would start the same work again while it was still running.
func admit(cfg *Config) error {
	paths, err := queued(cfg)
	if err != nil {
		return err
	}

	// The keys held by live jobs, and deliberately not activeKeys. activeKeys
	// counts queued requests too, so every request considered below would find its
	// own key there and none would ever be admitted. Live jobs need no such
	// self-exclusion, and they give the right end state for two queued requests
	// sharing a key: the first becomes a job, and the second then finds the key on
	// a live job and is dropped.
	//
	// Read once and kept up to date as jobs start, rather than re-read per entry:
	// the only thing which can add a key during this walk is a start below.
	live, err := liveJobs(cfg)
	if err != nil {
		return err
	}
	running := map[string]bool{}
	for _, job := range live {
		// liveJobs surfaces a job whose request never parsed, carrying an empty
		// Request, so Key() is empty for it. An empty key is no key and must not
		// be recorded as one, or it would match every request which gave none.
		if key := job.Request.Key(); key != "" {
			running[key] = true
		}
	}

	for _, path := range paths {
		// Work already running, so this request asks for nothing. Removed rather
		// than set aside or left queued: it is redundant and not broken, so there
		// is nothing to look at later, and leaving it would have every tick
		// reconsider it until the job it duplicates finished, then start it.
		//
		// Before the cap and not after it. A request is redundant whether or not
		// there is room, and at a cap of one this is unreachable below the cap check
		// for exactly the case it exists for: while a run holds the only slot, a
		// branch which moves again queues a request the cap steps away from, and the
		// tick after the run starts it, testing a tip already tested. Three merges
		// during one run measured as three further runs.
		if key := queuedKey(path); key != "" && running[key] {
			fmt.Printf("%s is already running, dropping %s\n", key, filepath.Base(path))
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				warnf("dropping %s: %v", filepath.Base(path), err)
			}
			continue
		}

		// Asked again on every entry, because startJob on a previous iteration
		// changed the answer. This is the cap, so it is the one thing here that
		// must not be hoisted out of the loop.
		live, err := liveJobs(cfg)
		if err != nil {
			return err
		}
		if len(live) >= cfg.MaxJobs {
			return nil
		}

		job, err := acceptRequest(cfg, path)
		if err != nil {
			// The entry having gone is the race acceptRequest anticipates in its
			// own comment: it removes the queue entry only once the job is
			// complete, so another tick, a hook or a hand can take the name from
			// under us. Nothing to say about it, and nothing to do.
			//
			// Lstat and not Stat, so a dangling symlink is not mistaken for this.
			// It reads as ENOENT and stats as ENOENT, so following the link would
			// call an anomaly normal and pass over it in silence on every tick.
			// Lstat sees the link, so it falls to the branch below and is said out
			// loud.
			if _, statErr := os.Lstat(path); errors.Is(statErr, fs.ErrNotExist) {
				continue
			}
			// Only a request whose own contents are the problem is set aside,
			// because nothing retries a rejected name and nothing prunes it. The
			// rest -- no space, a file field naming something not written yet, no
			// free job name -- may come right, so the request stays queued and a
			// later tick tries it again. Stepped over rather than returned on:
			// the entries behind it have done nothing wrong.
			if !errors.Is(err, errBadRequest) {
				warnf("%v", err)
				continue
			}

			// Out of the way means a name queued ignores, which is what a dot
			// gives us. A plain suffix would leave the file in the listing, so a
			// later tick would pick it up again, rename it again, and go on
			// lengthening the name until the kernel refused it.
			base := filepath.Base(path)
			warnf("%v, setting it aside", err)
			if err := renameIn(cfg.queueDir(), base, "."+base+".rejected", true); err != nil {
				warnf("setting %s aside: %v", base, err)
			}
			continue
		}

		// A startJob failure is ours, not the request's: it means we could not
		// fork, or could not open the job's log. Every remaining entry would
		// almost certainly fail the same way, so this returns rather than
		// carrying on. Do not "fix" this into a continue to match the branches
		// above; they are about one request being unusable, this is about us.
		if err := startJob(cfg, job); err != nil {
			return err
		}

		// Recorded as the job starts, so two requests sharing a key cannot both be
		// started inside one tick. The set read before the walk knows nothing about
		// a job this walk created.
		if key := job.Request.Key(); key != "" {
			running[key] = true
		}
	}

	return nil
}

// queuedKey is the dedup key of a queued request, or empty when it has none.
//
// A request which cannot be read or parsed is reported as having no key, and that
// is not an error here. Nothing unreadable is a duplicate of anything, and admit
// already has a path for it: acceptRequest fails with errBadRequest and the
// request is set aside, said out loud once. Failing here instead would swallow
// that, and complaining here as well would say it twice.
func queuedKey(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	req, err := parseRequest(data)
	if err != nil {
		return ""
	}

	return req.Key()
}

// openJobLog opens a job's log to append to.
//
// A function for the sake of its comment, which both callers need and only one
// carried. An *os.File and not any other writer: os/exec hands a file to the child
// directly, with no pipe and no copy goroutine. Anything else gets a pipe whose
// copy goroutine runs ReadFrom, and a bytes.Buffer's ReadFrom restores the length
// it saw before the read it is parked in, so a line written beside it disappears
// without a trace. Widening the return type here is how that regression comes back.
func openJobLog(job *Job) (*os.File, error) {
	return os.OpenFile(filepath.Join(job.Dir, jobLog),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// startJob starts the supervisor for a job, detached, and records its pid.
//
// A copy of ourselves rather than the hook directly: the supervisor has to outlive
// this tick, run three events in order and record the outcome, and a process of
// our own doing that is simpler than leaving a shell to.
func startJob(cfg *Config, job *Job) error {
	self := os.Getenv("E2E_ORCHESTRATOR_BIN")
	if self == "" {
		found, err := os.Executable()
		if err != nil {
			return err
		}
		self = found
	}

	log, err := openJobLog(job)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	cmd := exec.Command(self, "run-job", job.ID)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Stdin = nil
	// Its own session, so the supervisor survives this tick and is not taken down
	// with whatever cron does to our process group when we exit.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Floored at a second. The child's positive() refuses zero, so a sub-second
	// timeout truncated to it would kill the supervisor in loadConfig complaining
	// about a setting nobody wrote.
	timeout := int(cfg.HookTimeout.Seconds())
	if timeout < 1 {
		timeout = 1
	}

	// The settings this tick decided on, handed over rather than read again. The
	// supervisor would otherwise load the environment file for itself, so an edit
	// between the two would give one job a different root or cap from the tick
	// which admitted it, and a test whose config never came from a file at all
	// would give it no root to find.
	//
	// Appending is what makes these win over anything already in our own
	// environment: os/exec de-duplicates cmd.Env and keeps the last occurrence of
	// a name. Note that syscall.copyenv, which builds os.Environ, keeps the first
	// instead, so this is os/exec's rule and not Go's everywhere.
	cmd.Env = append(os.Environ(),
		"E2E_ORCHESTRATOR_BIN="+self,
		"E2E_ROOT="+cfg.Root,
		"E2E_HOOKS="+cfg.Hooks,
		"E2E_MAX_JOBS="+strconv.Itoa(cfg.MaxJobs),
		"E2E_DONE_KEEP="+strconv.Itoa(cfg.DoneKeep),
		"E2E_HOOK_TIMEOUT="+strconv.Itoa(timeout),
	)

	if err := cmd.Start(); err != nil {
		return err
	}
	// A pid we could not record is not a reason to stop admitting. The supervisor
	// is already running, so returning here would report a failure for a tick which
	// succeeded and would leave the rest of the queue waiting.
	//
	// ErrNotExist is silent because it means the job got there first: a veto, or a
	// run hook of "true", can finish and rename the directory out from under this
	// write. What it costs in the other cases is a live job with no pid file, which
	// liveJobs cannot count: the cap can then be exceeded within this very walk, so
	// more run hooks are going at once than E2E_MAX_JOBS allows, which on a host
	// where the cap is the only thing keeping two runs off one set of test VMs is
	// what the warning is for. The undercount lasts only until the job finishes, and
	// the alternative is admitting nothing at all.
	if err := job.recordPid(cmd.Process.Pid); err != nil && !errors.Is(err, fs.ErrNotExist) {
		warnf("%s: cannot record pid %d: %v", job.ID, cmd.Process.Pid, err)
	}

	// Nothing waits for it. The pid and its start time are how a later tick tells
	// whether it is still going.
	go func() { _ = cmd.Wait() }()

	fmt.Printf("%s started as pid %d\n", job.ID, cmd.Process.Pid)

	return nil
}

// runJob supervises one job to its end. This is what tick starts detached.
func runJob(cfg *Config, id string) int {
	// Before markStarted, deliberately. A job whose request is missing, empty or
	// unparseable has to fail here, while it is not started and while this exiting
	// process is the only thing making it look alive, so it holds no slot and the
	// next sweep can dispose of it.
	job, err := readJob(filepath.Join(cfg.jobsDir(), id))
	if err != nil {
		warnf("%v", err)
		return 1
	}

	log, err := openJobLog(job)
	if err != nil {
		warnf("%v", err)
		return 1
	}
	defer func() { _ = log.Close() }()

	// A job with no run hook is a job with no work, and doc.go is explicit that of
	// the run event exactly this is the job. Recorded as a success it would be a
	// nightly reporting PASS having tested nothing, which is the one failure shape a
	// nightly must not have -- and the poll hook has already written down that it
	// asked, so nothing would ask again for a day. An unexecutable 50-e2e-tests, or
	// a hooks.d with no run/ in it at all, is how this happens on a real host.
	//
	// warnf as well as the log, because the job log is not what anybody reads after
	// a run which claims to have passed; the tick's own output is.
	//
	// The listing happens again in runHooks below, so a file passed over for not
	// being executable is named twice in the log of a job which does have work. The
	// alternative is taking runHooks apart to hand it a list, and saying it twice is
	// the cheaper of the two.
	work, err := hooksFor(cfg, eventRun, log)
	if err == nil && len(work) == 0 {
		err = fmt.Errorf("no run hook in %s, so there is no work to do",
			filepath.Join(cfg.Hooks, eventRun))
	}
	if err != nil {
		warnf("%s: %v", job.ID, err)
		fmt.Fprintf(log, "run: %v\n", err)
		if err := job.finish(cfg, 1); err != nil {
			warnf("%v", err)
		}
		return 1
	}

	// Cancelled before anything was done. Not an error and not retried: somebody
	// decided this should not run.
	status, err := runHooks(cfg, eventPreRun, job, nil, log)
	if err != nil {
		fmt.Fprintf(log, "pre-run: %v\n", err)
		status = 1
	}
	if status != 0 {
		fmt.Fprintf(log, "cancelled before starting\n")
		if err := job.finish(cfg, status); err != nil {
			warnf("%v", err)
			return 1
		}
		return status
	}

	// From here the work has begun, so a sweep must not queue this again.
	if err := job.markStarted(); err != nil {
		fmt.Fprintf(log, "%v\n", err)
		return 1
	}

	status, err = runHooks(cfg, eventRun, job, nil, log)
	if err != nil {
		fmt.Fprintf(log, "run: %v\n", err)
		status = 1
	}

	if _, err := runHooks(cfg, eventPostRun, job,
		map[string]string{"E2E_STATUS": strconv.Itoa(status)}, log); err != nil {
		fmt.Fprintf(log, "post-run: %v\n", err)
	}

	if err := job.finish(cfg, status); err != nil {
		warnf("%v", err)
		return 1
	}

	return status
}

// emitCommand is the emit subcommand: dispatch hooks for an event in the calling
// job's context, as "$E2E_ORCHESTRATOR" emit <event> [key=value ...].
//
// Fan-out and nothing else. The orchestrator dispatches and forgets; it keeps no
// last value and has nothing to be asked for one. A producer whose progress some
// known reader wants writes that state itself.
func emitCommand(cfg *Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("emit wants an event to emit")
	}

	event := args[0]
	extra := map[string]string{}
	for _, arg := range args[1:] {
		name, value, found := strings.Cut(arg, "=")
		if !found {
			return fmt.Errorf("%q is not a field", arg)
		}
		extra["E2E_"+strings.ToUpper(name)] = value
	}

	var job *Job
	if id := os.Getenv("E2E_JOB"); id != "" {
		if found, err := readJob(filepath.Join(cfg.jobsDir(), id)); err == nil {
			job = found
		}
	}

	_, err := runHooks(cfg, event, job, extra, os.Stdout)

	return err
}
