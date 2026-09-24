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

// Command e2e-orchestrator is a scheduler. It keeps a queue of requests, admits
// them as slots free up, and supervises each one to its end. It knows nothing
// about testing, or about any other kind of work: the work is done by hooks, and
// this is the contract for writing one.
//
// The model is five words:
//
//   - Request. What somebody wants done, as a file of key=value lines.
//   - Job. An accepted request, with an identity and a directory.
//   - Slot. Permission to have a run hook executing. E2E_MAX_JOBS bounds them.
//   - Event. A point in a job's life, or a tick of the clock.
//   - Hook. An executable run in reaction to an event.
//
// The orchestrator owns requests, jobs and slots. Everything else is a hook.
//
// Everything on disk lives under E2E_ROOT: queue/ holds requests, jobs/ holds
// running jobs, done/ holds finished ones and state/ is the hooks' own. Hooks live
// under E2E_HOOKS, which defaults to <root>/hooks.d.
//
// # A hook
//
// A hook is an executable file in <hooks>/<event>/. The hooks of an event all run,
// in lexical order of their names, which is what the conventional two-digit prefix
// is for. A missing event directory is no hooks and no complaint, run excepted: see
// below.
//
// A file in such a directory is not a hook if any of this is true of it:
//
//   - it is a directory;
//   - its name contains a ".";
//   - its name begins with "." or "#", or ends with "~";
//   - no execute bit is set on it.
//
// All but the first two are reported, as "<event>: ignoring <name>[, why]", so a
// hook which is not running says why; a name beginning with "." is passed over in
// silence, being an editor's or a producer's own business.
//
// The dotless rule is not fussiness. An emacs backup keeps the file mode of what it
// backs up, so editing an executable 10-poll-branch in place leaves an executable
// 10-poll-branch~ beside it, and a rule which only skipped dotted names would run
// both the hook and a superseded copy of it as policy. Hooks are conventionally
// named without a dot, so that rule alone would be no protection at all here. The
// same list governs what is never read as a request.
//
// A hook may be written in anything. Nothing in the orchestrator's path is a shell:
// it parses requests itself and passes bytes through execve. A shell hook must
// therefore quote what it expands, since a _JSON value contains " and \.
//
// Every hook but a run hook is killed at E2E_HOOK_TIMEOUT (300 seconds by default):
// SIGTERM to its process group, SIGKILL three seconds later. A run hook has no
// timeout, work taking hours being the normal case.
//
// # The four events
//
// tick is the clock, dispatched by "e2e-orchestrator tick" before anything is
// admitted. Its hooks may submit requests, which the same tick then considers. It
// has no job, so it gets none of the per-job environment. The tick holds a lock
// while its hooks run, so a slow tick hook holds up the next tick.
//
// pre-run is a job which has been admitted and holds a slot, with no work begun.
// It is dispatched by the job's own supervisor, a detached process of the
// orchestrator's, and it is the one vetoable event: this is where an admission
// policy belongs, a check of who asked, of whether now is a good time, of whether
// the request still makes sense.
//
// run performs the work. Exactly this is the job. Also dispatched by the
// supervisor, unbounded in time. It is singular in purpose but not in count: if
// several run hooks are present they run in order. A project with one kind of work
// has one run hook.
//
// It is the one event where having no hooks is a failure rather than nothing to
// report. A job with no run hook is a job with no work, and recording it as a
// success would have a nightly report a pass having tested nothing, which is the one
// outcome nobody checks. Every tick says so as well, on standard error, because a
// job's supervisor is detached with the job log as both its streams: nothing it
// writes reaches whatever started the tick, and a job which fails this way publishes
// nothing, so its status file would otherwise be the only trace.
//
// post-run is the work having finished. Dispatched by the supervisor once the run
// hooks are done, with their status in E2E_STATUS. Nothing waits on it and nothing
// acts on what it says; it is where reacting to an outcome belongs.
//
// Detail lives in the environment rather than in more event names.
//
// # Exit codes
//
// What a non-zero exit means depends on the event, and the difference is whether
// the status is a decision about a job or advice:
//
//   - tick: logged and ignored. The remaining tick hooks still run, and the tick
//     goes on to sweep and admit whatever the status was.
//   - pre-run: a veto. The sequence stops at the first non-zero, no run and no
//     post-run hook runs for that job, and the job is recorded with that status
//     and is not retried. A veto is a decision, not a fault.
//   - run: zero is the job having succeeded, non-zero its having failed. The
//     sequence stops at the first non-zero, so a later run hook does not run, and
//     that status becomes the job's. post-run still runs. A job with no usable run
//     hook is the exception: it is recorded as failed without pre-run or post-run
//     having run at all, there being no work for either to be about.
//   - post-run: logged and ignored. The remaining post-run hooks still run, and
//     the job's recorded status stays the run hooks'.
//
// So: only pre-run and run stop the sequence, and only they decide anything.
//
// A hook which could not be started at all, and one killed at its timeout, count
// as exit 1. A hook killed by a signal counts as 128 plus the signal.
//
// # The environment
//
// A hook inherits the environment the orchestrator was started with, which is how
// a settings file reaches hooks without the orchestrator knowing any of its names,
// and then gets these:
//
//   - E2E_EVENT: the event name.
//   - E2E_HOOKS: the hook root, so a hook can find its siblings.
//   - E2E_QUEUE: the queue directory, for a hook which writes a request into it
//     itself. A tick makes it, with E2E_STATE, before any hook of that tick runs, so
//     a tick hook can count on it. The events a job's supervisor dispatches have it
//     because the tick which admitted that job made it, which is transitive rather
//     than promised: a run-job invoked by hand on a root whose queue/ has since been
//     removed has no queue directory. A hook writing there itself should make it
//     first; request makes it in any case.
//   - E2E_STATE: a directory a hook may keep its own state in, persistent across
//     ticks and jobs. It exists by the time any hook runs, whatever the event and
//     whatever dispatched it.
//   - E2E_ORCHESTRATOR: the path to this binary, so a hook can ask for work with
//     request or dispatch an event of its own with emit. See below.
//
// The per-job events (pre-run, run, post-run, and anything emitted from within a
// job) additionally get:
//
//   - E2E_JOB: the job identity, unique and stable for the job's life.
//   - E2E_JOB_DIR: the job's own directory. It holds the request, the log, the pid
//     of the supervisor, a marker saying the work began, and at the end the status.
//   - E2E_LOG: the job's log, which is also where a hook's own output is captured.
//     A tick hook has no E2E_LOG, there being no job to have one; its output is
//     captured to the tick's own output.
//   - every field of the request, verbatim, with "E2E_" prefixed and the name
//     upper-cased and its encoding suffix intact. So branch=main arrives as
//     E2E_BRANCH, prs_JSON as E2E_PRS_JSON and data_FILE as E2E_DATA_FILE, which
//     lets a hook tell a scalar from JSON from a file by the name alone.
//
// post-run also gets E2E_STATUS, the run hooks' status in decimal.
//
// E2E_ATTEMPT is not set by the orchestrator. A job queued again after its
// supervisor died before the work began carries the field attempt=2, so such a job
// has E2E_ATTEMPT=2 and a first attempt has the variable unset.
//
// The request's own fields are added first and the orchestrator's values after
// them, and the last duplicate is what the process gets, so a request field called
// log cannot redirect E2E_LOG. Fields given to emit are added last of all, since
// they are the emitter's own detail about the event.
//
// # The request format
//
// A request is a text file of key=value lines. Blank lines and lines beginning with
// "#" are ignored. A key matches [A-Za-z_][A-Za-z0-9_]* and everything after the
// first "=" is the value. Lines are trimmed of surrounding whitespace, so there is
// no space around the "=" and no value ends in one.
//
// A value is a single line. The format has no way to write a newline in one, which
// is what keeps it trivially readable by any language and by a person with cat.
//
// There are three encodings, and a field's encoding is fixed by whoever defines
// the field, never chosen per request, so a consumer of a field handles exactly one
// form:
//
//   - name=value is a scalar.
//   - name_JSON=value is compact JSON. It is validated when the request is read,
//     and malformed JSON rejects the whole request.
//   - name_FILE=path is bulk or binary data in a file. The orchestrator copies the
//     file into the job directory when the request is accepted and rewrites the
//     value to point at the copy, named for the field in lower case, so data_FILE
//     becomes <job dir>/data_file. A producer may therefore write a temporary file
//     and forget about it, and a job is a complete account of what was asked for.
//
// Use a scalar unless the datum has structure, _JSON when it does, and _FILE when
// it is large: an environment variable is capped near 128KB on Linux, which is the
// practical boundary between the second and the third.
//
// Two fields the orchestrator reads:
//
//   - key is the dedup key. See below.
//   - name is a suggested job identity, used when it matches
//     [A-Za-z0-9_][A-Za-z0-9_-]*. A dash is allowed anywhere but first, the name
//     being something a consumer of it may pass on as a command line argument. A
//     request with no usable suggestion is named for the UTC time it was accepted
//     instead. Either way, a name already taken in jobs/ or done/ gets a counter
//     appended, so identity is the orchestrator's to assign and is unique.
//
// Reserved names, refused in a submitted request rather than quietly overwritten:
// job, queued_at and started_at. The orchestrator sets job and queued_at itself in
// the request it stores with the job, so a hook sees them as E2E_JOB and
// E2E_QUEUED_AT; started_at is held in reserve and nothing sets it yet. attempt is
// deliberately not reserved, a requeued request carrying it; forging it only denies
// that request a retry.
//
// # Submitting a request
//
// Two ways, and a hook has both:
//
//	"$E2E_ORCHESTRATOR" request key=value ...
//	printf 'key=value\n' > "$E2E_QUEUE/.tmp.$$" && mv "$E2E_QUEUE/.tmp.$$" "$E2E_QUEUE/$(date +%s)-$$"
//
// Each argument of the first form is one line of the request. It prints the name of
// the queue entry it wrote on standard output; if the key was already asked for it
// says so on standard error, prints nothing on standard output, and exits zero.
//
// So standard output is how a producer tells the two apart, and the exit status is
// not: a name means queued, nothing means dropped, and both are zero because both
// are ordinary. A producer which keeps a note of what it has asked for should write
// that note only on a name, the dropped request having asked for nothing.
//
// Prefer the first. It is the only one of the two which can see that the same key is
// already waiting in the queue, which is what a producer asking on every tick
// depends on; see The dedup key below.
//
// The second form is the whole reason the queue is a directory: anything that can
// write a file and rename it can create work, with no library and no linking. Write
// to a dotted temporary name within the queue directory and rename it in. Dotted
// because the orchestrator never reads a dotted name as a request, and within that
// directory because a rename within one directory is atomic, while a rename across
// filesystems is a copy and can be seen half done. Between the two, a request being
// written is never visible.
//
// A queue entry's name is otherwise free-form, but the queue is served oldest first
// by lexical order of names, so name an entry for when it was asked for. The
// orchestrator names its own entries with a nanosecond timestamp and a pid.
//
// # The dedup key
//
// The field key, when a request carries one, is the dedup key. A request whose key
// is already queued or already running is dropped, which is not an error: the point
// is that a producer can ask on every tick without keeping track of what it asked
// for.
//
// The key is opaque. The orchestrator compares it and reads nothing into what it
// says, so what counts as the same work is the producer's business.
//
// It is enforced in two places, and both are needed.
//
// On admission, against the keys of running jobs. The drop runs whether or not there
// is room to start anything: a request duplicating running work asks for nothing
// either way, and at a cap of one a check made only when there is room would be
// unreachable for precisely the case which produces such requests -- a branch moving
// again while the run of its last revision is still going.
//
// And on submit, against the keys of everything queued as well as everything
// running. Only "e2e-orchestrator request" goes through submit; a request renamed
// into the queue does not, which is why admission checks at all. So the two ways of
// asking are not quite equivalent, and that difference is the queued half: without
// it, a branch moving three times during one run leaves three queued requests, and
// each is admitted in turn to test a revision the one before it has already tested.
//
// Enforcement is best-effort in either case: two producers can both pass the check
// before either writes.
//
// # E2E_ORCHESTRATOR
//
//	"$E2E_ORCHESTRATOR" request <key=value ...>
//	"$E2E_ORCHESTRATOR" emit <event> [key=value ...]
//
// E2E_ORCHESTRATOR holds the path to this binary and nothing else -- not a
// ready-made command -- so that a hook can quote it like every other variable; a
// value of two words would have to be left unquoted, contradicting the rule above.
// Every hook gets it, a tick hook included, which is what lets the hook that decides
// whether to run ask through submit rather than by writing into the queue. Without a
// condition: in the one case where the orchestrator cannot tell where its own binary
// is, it says so on standard error rather than leaving the variable quietly unset.
//
// request submits, as under Submitting a request above.
//
// emit dispatches the hooks of <event> in the calling job's context, which it takes
// from E2E_JOB in its environment. The event may be any name; its hooks are the
// executables in <hooks>/<event>/ and they are bounded by the hook timeout like
// every event but run. Fields given here arrive prefixed and upper-cased like a
// request's.
//
// emit exists so that a job which knows its own internal milestones can expose them
// without the orchestrator having to model them, and it is optional in both
// directions: a hook that never emits is normal, and a run hook which does should
// tolerate E2E_ORCHESTRATOR being unset, so that the same script can be run by hand.
// A hook whose whole purpose is to ask for work cannot tolerate it, and should say so
// and fail rather than fall back to writing into the queue, which asks for something
// slightly different.
//
// emit is fan-out, and keeps no state. The orchestrator dispatches and forgets: it
// does not remember the last value emitted and has nothing to ask it for, and it
// discards what the hooks of an emitted event exit with. A producer whose progress
// some known reader wants to display writes that state to a file itself, under
// E2E_STATE or in the job directory, and emits in addition for readers nobody has
// written yet.
package main
