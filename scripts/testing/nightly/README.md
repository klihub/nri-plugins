# Nightly e2e test runs

`e2e-runner` runs the end-to-end test suite the way a nightly does: it fetches
the branch to test, builds it, runs the tests in the test VMs, collects what
they leave behind, reports on it and publishes the report. `e2e-report serve`
serves what it published. `e2e-orchestrator` decides when a run happens and what
happens around it, and is what cron drives.

This describes how to set the three up on a host. For the tests themselves, and
for the coverage they collect, see [`test/e2e/README.md`](../../../test/e2e/README.md).

## What the host needs

- `git`, GNU `make`, `go` (the version in `go.mod`), `docker` with `buildx`
- `vagrant` and `qemu-system-x86`, for the test VMs
- `ansible`, which the framework provisions the VMs with
- `tar` and `xz`, for packing up the artifacts of the tests
- disk: a clone and a worktree per run, a couple of gigabytes of VM images, and
  the published results, some 20M per run browsable or 2.5M packed

The user running it has to be able to run `vagrant`, `qemu` and `docker`.

## A run

```shell
scripts/testing/nightly/e2e-runner [options] [<e2e-tests>]
```

A run clones the remote repository if it has not been cloned yet, fetches it if
it has, adds a worktree of the branch to test, and re-executes the `e2e-runner`
of the revision under test from there. It then builds the plugins, runs the
tests, destroys the test VMs, collects the results into a directory of its own
under the result root, generates the coverage report and the report of the run,
and removes the worktree.

It does nothing else. Whether to run at all, pointing `latest` at what just
finished, and removing what is too old to keep are not a test runner's job: they
are the orchestrator's hooks, described under [From cron](#from-cron).

What it defaults to, all of it overridable on the command line or from the
environment:

| variable | option | default |
| --- | --- | --- |
| `REMOTE_REPO` | `--remote` | `https://github.com/containers/nri-plugins` |
| `TEST_BRANCH` | `--branch` | `main` |
| `CLONED_REPO` | `--local` | `/opt/e2e-test/nri-plugins/nri-plugins` |
| `RESULT_ROOT` | `--results` | `/opt/e2e-test/nri-plugins/results` |
| `RESULT_NAME` | `--name` | the UTC date and time, `2026-09-16-02-00` |
| `KEEP_ARTIFACTS` | `--keep-artifacts` | `failed`, the rest packed per test |
| `KEEP_COVERAGE_DATA` | `--keep-coverage-data` | off, the merged data is kept |
| `PACK_RESULTS` | `--pack-results` | off, see below |
| `K8SCRI` | `--runtime` | containerd and cri-o on alternating days |
| `FULL_BUILD` | `--full-build`, `--minimal-build` | everything once a week |
| `OWN_TOOLING` | `--own-tooling` | off, the tested revision's tooling runs |

Anything left over on the command line is the test set to run, the whole default
set if there is none.

The exit status says whether the runner did its job, not whether the tests
passed: a run whose tests failed is a run which went fine. The verdict is the
first word of `status.txt` of the run, and `results.json` has the details.

**One run at a time per result root.** A run holds a lock in the root for as
long as it lasts, because two runs there would share a worktree, fight over the
test VMs and publish over each other. A second run refuses to start and says so.
The lock is a file descriptor, so the kernel drops it however a run ends -- there
is no stale lock to clear after a crash, and nothing to reason about a pid which
may since have been given to something else.

Scheduled runs are kept apart by the orchestrator's cap instead, which is one, so
the lock never comes into it there. It is kept because it is the only thing
standing between a run started by hand and a scheduled one starting on top of it.

## From cron

Cron drives `e2e-orchestrator`, not the runner. The orchestrator owns a queue of
requests, a cap on how many runs may be going at once, and a set of hooks. A test
run is one of those hooks; deciding to run, pointing `latest` at what finished and
applying retention are others. `e2e-cron-job` is a thin front for it: the settings
file, the proxies, and then whatever arguments it was given. So one crontab line
ticks and another submits the nightly.

**Deploy the whole of this at once.** The runner no longer has the options the
older crontab line passed it and no longer decides anything for itself, so a host
left on the old `*/5` line asks for an unconditional full run every five minutes.
The lock serialises them, so it is not a pile-up, but it is back-to-back full runs
instead of one a night and nothing says so. If it has to land in pieces, drop the
`*/5` line first and put the new crontab in last. Once this much is deployed the
old line is inert rather than dangerous: `e2e-cron-job` with no argument now says
what it wants and exits 2 without running anything.

### Deploying the tree to run from

The binary is installed; the policy is not. `e2e-cron-job`, the hooks and
`e2e-runner` are read off disk every tick, so a working checkout which somebody
pulls into, switches branches in or rebases is a deployment which changes
underneath itself, as easily mid-run as between runs. Point cron at a tree nobody
works in.

Nothing has to be configured for that, which is the happy part. Every path in the
deployment is already relative to the file it is written in rather than to a
repository: the runner takes `--own-tooling`'s tree as three levels up from itself,
the run hook takes the runner as its own `../../e2e-runner`, and `e2e-cron-job`
takes the orchestrator as `../../../build/bin/e2e-orchestrator` and the hooks as the
`hooks.d` beside it. What any of this needs is not the repository but a tree of the
right shape.

A worktree detached at the revision to run is the tidiest way to get one:

```shell
clone=/opt/e2e-test/nri-plugins/nri-plugins
deployed=/opt/e2e-test/nri-plugins/deployed
revision=v0.9.4                                 # or $(git -C "$clone" rev-parse main)

git -C "$clone" worktree add "$deployed" "$revision"
(cd "$deployed" && make e2e-orchestrator)
```

A tag, or a commit which is on a branch anyway, in preference to a sha1 no ref
reaches: then a ref of its own is what keeps the objects alive and the worktree's
`HEAD` is a second belt rather than the only thing holding them.

The crontab then points at
`$deployed/scripts/testing/nightly/e2e-cron-job`. Three things recommend it. It is
detached, so a pull, a branch switch or a `reset --hard` anywhere else cannot move
it. `git -C "$deployed" rev-parse HEAD` answers what is actually running without
anybody having had to write it down, and the runner records that same revision as
`git.runner` in everything it publishes. And an update is one deliberate
`git -C "$deployed" checkout <new revision>` and a rebuild.

A linked worktree does share the object store with the clone: its `.git` is a file
pointing into the clone's `.git/worktrees/`, so the clone is part of the deployment.
Removing the clone leaves the files in place and the cron job still running -- what
stops working is asking what is running, and updating it. `git worktree remove` in
the clone, on the other hand, deletes the deployed tree outright. `git gc` there is
safe, including `--prune=now` with no branch left pointing at the deployed revision:
the `HEAD` of another worktree is a reachability root. Only while the entry lasts,
mind: a deployed directory which disappears without `git worktree remove` leaves a
stale entry, which `git gc` prunes after three months (`gc.worktreePruneExpire`,
unset here), and an unreferenced revision can be collected once it goes. No hazard to
a live deployment, but one to working out months later what a dead one had been
running, which is the other reason to pin a tag.

If the deployment should not depend on git at all, extract the revision instead:

```shell
mkdir -p "$deployed"
git -C "$clone" archive "$revision" | tar -x -C "$deployed"
git -C "$clone" rev-parse "$revision" > "$deployed/DEPLOYED_SHA"
(cd "$deployed" && make e2e-orchestrator)
```

Same effect and nothing shared, at the price of writing the provenance down rather
than being able to ask for it: with no repository there, the runner records no
`git.runner` either.

Three things to get right whichever way the tree is made.

- **It has to be the whole tree**, and not `scripts/testing/nightly` copied out of
  it. `make e2e-orchestrator` needs the Go module, and `--own-tooling` looks for
  `test/e2e/cmd/e2e-report` under the tree the runner was started from and refuses
  the run when it is not there.
- **The deployed tree is not `E2E_CLONE`.** The two look interchangeable in the
  settings file and are not: the clone is where the runner fetches and makes its
  test worktrees, and it should go on updating. Only the scripts, the hooks and the
  binary want pinning. A worktree of that clone, as above, is fine; it is a
  different directory doing a different job.
- **Rebuild after an update.** `E2E_ORCHESTRATOR` defaults to the `build/bin` of the
  deployed tree, so a `checkout` without a `make e2e-orchestrator` leaves
  yesterday's binary running today's hooks.

`--own-tooling` is where a pinned tree pays for itself twice: the tooling which
runs is then the one you deployed, testing the branch, rather than the branch's own
copy of the tooling testing itself.

### Setting it up

`$PWD` below is the tree cron reads every tick, so make it the deployed one from
above rather than a checkout you work in.

```shell
sudo install -m 644 -D scripts/testing/nightly/e2e-cron-job.env \
    /etc/sysconfig/nri-plugins-e2e-cron-job
sudo "${EDITOR:-vi}" /etc/sysconfig/nri-plugins-e2e-cron-job

make e2e-orchestrator

cron=$PWD/scripts/testing/nightly/e2e-cron-job
(crontab -l 2>/dev/null; cat <<EOF
PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin
*/5 * * * * $cron tick >>\$HOME/e2e-cron-job.log 2>&1
2 2 * * *   $cron request key=nightly name=test-\$(date -u +\%Y-\%m-\%d-\%H-\%M) trigger=nightly >>\$HOME/e2e-cron-job.log 2>&1
0 3 * * 0   docker image prune -a -f --filter 'until=168h'
EOF
) | crontab -
```

Three lines: do what is due every five minutes, ask for a run at two in the
morning whatever the branch has been doing, and clean up old docker images. The
crontab is the test user's, since the run needs `vagrant`, `qemu` and `docker` and
not root; the settings file goes in `/etc/sysconfig` all the same, which takes one
`sudo` and is where `e2e-cron-job` looks by default. Nothing then has to name the
path -- not the crontab, and not you at a prompt.

**Two things about that block are easy to get wrong, and were got wrong here
before.**

- Cron does not expand variables in a crontab environment line. `$cron` above is
  expanded by the shell which writes the crontab, which is why the heredoc is
  unquoted. Anything written into the crontab as an environment line has to be
  literal: `E2E_CRON_ENV=$HOME/...` there stays those very characters and the
  settings file is never found, which is the other reason the file lives where the
  script already looks. A command is different -- cron runs it through a shell --
  so `\$HOME` and `\$(date ...)` are escaped here on purpose and expanded when the
  line runs.
- `%` is a field separator in a crontab command, so every `%` of a `date` format is
  written `\%`. Unescaped, the command ends at the first one and what follows
  becomes input to it, so the request is submitted with half a name.

The nightly line says nothing about which remote or branch. Everything a request
leaves out comes from the settings file, which is the one place a host is
configured; a field on the request overrides the file for that job, which is how a
one-off tests something else:

```shell
scripts/testing/nightly/e2e-cron-job request key=manual branch=my-topic trigger=by-hand
```

**The nightly line and `E2E_FORCE_AFTER` overlap, and a host wants one of them, not
both.** They used to be one thing: both crontab lines ran the same runner and shared
one state file, so the nightly reset the polling clock and a 24 hour force-after
never fired inside the following day. One run a day on a quiet branch. The nightly
is a request with a `key` of its own now, and polling keeps its own book, so on a
branch which has not moved a host gets the nightly at two in the morning *and* a
forced poll run a day after the last poll asked for anything. Two multi-hour runs a
day, on a host which does one at a time.

So pick one. Keep the nightly line and set `E2E_FORCE_AFTER=never`, for a run at a
predictable hour; or drop the nightly line and leave force-after alone, for a run
within a day of the last one whenever that falls. The nightly carries a `key` of its
own so that it is never dropped as a duplicate of a queued poll; if both do arrive,
the cap runs them one after the other.

A drop-in in `/etc/cron.d` works as well, and takes the user to run as in a field
of its own after the five of the schedule. Name the user there: every example of
the format says `root`, and this needs none of it.

### What a tick does

`e2e-orchestrator tick` is invoked, does what is due, and exits. Nothing runs
between ticks and there is no daemon. In order, a tick

1. makes `queue/` and `state/`, so that a hook can count on both,
2. runs the `tick` hooks, which may submit requests this same tick will consider,
3. sweeps jobs whose `run` hook is gone without having recorded an outcome,
4. says so if there is no usable `run` hook, that being the one thing a job could not
   tell anybody itself,
5. admits the oldest eligible request while fewer jobs are live than the cap, and
   runs the `pre-run` hooks for it, and
6. starts the `run` hooks detached, records the pid, and does not wait.

There are four events, then: `tick`, `pre-run`, `run` and `post-run`. A non-zero
exit from a `pre-run` hook is a veto, and the job is discarded as cancelled rather
than retried, which is where an admission policy belongs -- a check of who asked, or
of whether now is a good time. None ships, and the `pre-run` directory need not
exist.

A job outlives the tick which started it, and that is safe because a job's state is
its directory: a recorded pid which is gone is a job which is over, corroborated
against the process start time so a recycled pid is not mistaken for a live one. A
machine rebooted mid-run is tidied up by the next tick rather than by anything
having survived.

**One tick at a time.** A tick takes a lock on `$E2E_ROOT/.tick.lock` and exits
quietly if another tick holds it. Without it, two overlapping ticks would each
count the live jobs, each see room and each admit, so a cap of one would start two
runs; and two sweeps could requeue one job twice. Overlapping is not hypothetical:
`E2E_HOOK_TIMEOUT` is 300 seconds, which is exactly the five minutes between ticks,
and it bounds each hook rather than the tick, so several hooks and a sweep add up to
a tick which can outlive its own timer without anything having gone wrong. The lock
is held for the seconds a decision takes, on a file descriptor, so the kernel drops
it however the tick ends.

**The cap ships at 1**, which is what the runner's lock already enforced, and
raising it needs work first: `vm_name` does not carry the job, so two concurrent
runs of one matrix would want the same test VM, and the runner's own lock would
refuse the second run anyway. It is also a flake knob rather than only a throughput
knob -- the tests wait on readiness with timeouts, on the very CPUs a second run
would contend for -- so what it may safely be on a host is a measurement and not
an estimate.

### The hooks

A hook is an executable file in `<hooks>/<event>/`, and the hooks of an event run
in the lexical order of their names, which is what the two-digit prefix is for.
`E2E_HOOKS` says where they are; left unset, `e2e-cron-job` points it at the
`hooks.d` beside itself, which is where the shipped ones are.

The contract a hook of your own is written against is the package comment of
`test/e2e/cmd/e2e-orchestrator`: `go doc ./test/e2e/cmd/e2e-orchestrator`.

| hook | event | what it does |
| --- | --- | --- |
| `tick/10-poll-branch` | `tick` | asks the remote what the branch is at, in one `git ls-remote`, and submits a request if it moved since the last tick or if `E2E_FORCE_AFTER` has passed with nothing new |
| `run/50-e2e-tests` | `run` | the work: invokes `e2e-runner` |
| `post-run/50-publish-latest` | `post-run` | points `latest` at the run which just finished, relatively, which is what the results server can follow |
| `post-run/90-prune-results` | `post-run` | applies retention to the published results |

`10-poll-branch` reads `E2E_REMOTE`, `E2E_BRANCH`, `E2E_FORCE_AFTER` and
`E2E_PROXIES`, and keeps what it last saw under `$E2E_ROOT/state`. It submits with
a `key` naming the remote and the branch, which is what lets it submit on every
tick without ever queueing the same work twice. It also needs `E2E_ORCHESTRATOR` and
`E2E_STATE`, both of which the orchestrator sets for every hook; run by hand without
them it says which one is missing and exits 1 rather than guessing.

**A merge during a run is tested after it, not skipped and not tested twice.** The
polling hook writes down a revision only when a request for it really was queued, so
while a run is going its request is refused as a duplicate and it records nothing: it
says so and asks again on the next tick. The branch therefore still looks moved when
the run ends, and the tick after that queues one request for the tip. Three merges
during one run are one further run, of the last of them. This is why the state file is
written after the request and not before it, which is the opposite of what the
hook used to do and is commented where it happens.

`50-e2e-tests` reads `E2E_REMOTE`, `E2E_BRANCH`, `E2E_RESULTS`, `E2E_CLONE`,
`E2E_PROXIES`, `E2E_PACK_RESULTS`, `E2E_OWN_TOOLING`, `E2E_OPTIONS` and
`E2E_TESTS`, and names the run after the job. It is the only file which knows both
the orchestrator's contract and the runner's command line, which is where that
knowledge belongs: different work is a different file there and nothing else
changes.

The two `post-run` hooks read `E2E_RESULTS`, and pruning reads
`E2E_RETENTION_DAYS` and `E2E_RETENTION_KEEP`. Running after a job rather than
inside one is what makes a machine which has not tested lately tidy up anyway.

A name beginning with `.` or `#`, ending with `~`, or containing a `.` is not a
hook. That is what makes editing one in place safe: an editor leaves a backup
beside the file and emacs preserves its mode, so without the rule a superseded copy
of the polling hook would be submitting requests beside the real one.

Polling asks the remote with `GIT_TERMINAL_PROMPT=0`, so a remote wanting
credentials fails instead of sitting on its prompt for the whole hook timeout with
the tick lock held. That covers https only, and an `ssh://` remote can still block
on a passphrase, so a host which polls over ssh wants this in the settings file too:

```shell
GIT_SSH_COMMAND='ssh -o BatchMode=yes'
```

### Settings

One file, `/etc/sysconfig/nri-plugins-e2e-cron-job`, or wherever `E2E_CRON_ENV`
says. `e2e-cron-job` sources it with `set -a`, so everything in it is exported: the
orchestrator picks out the settings which are its own and hands its environment to
every hook, which pick out theirs. Neither side has to know the other's names, and
there is one file to edit rather than one per hook.

That file is authoritative for the host, and it wins over the environment rather
than the other way round: sourcing assigns, so `E2E_BRANCH=my-topic e2e-cron-job
request ...` is quietly ignored and the file's branch is tested. **The per-job
override is a request field**, which does win, `hookEnv` appending the request after
the environment. So the way to test something else once is
`request branch=my-topic`, as above, and not a variable in front of the command.

| setting | default | read by |
| --- | --- | --- |
| `E2E_ROOT` | none, and it has to be set | the orchestrator: where `queue/`, `jobs/`, `done/` and hook state live |
| `E2E_HOOKS` | the `hooks.d` beside `e2e-cron-job` | the orchestrator |
| `E2E_MAX_JOBS` | `1` | the orchestrator: the cap |
| `E2E_HOOK_TIMEOUT` | `300` seconds | the orchestrator: per hook, except `run`, which is unbounded |
| `E2E_DONE_KEEP` | `200` | the orchestrator: finished job directories kept |
| `E2E_REMOTE` | upstream, for polling; set in the shipped file | polling, and the run |
| `E2E_BRANCH` | `main`, for polling; set in the shipped file | polling, and the run |
| `E2E_FORCE_AFTER` | `24h`, empty for never, `0` for every tick | polling |
| `E2E_RESULTS` | none; set in the shipped file | the run, `latest`, retention |
| `E2E_RETENTION_DAYS` | `100`, `0` keeps everything | retention |
| `E2E_RETENTION_KEEP` | `10` newest runs, whatever their age | retention |
| `E2E_CLONE` | `/opt/e2e-test/nri-plugins/nri-plugins` | the run |
| `E2E_PROXIES` | none | polling, and the run |
| `E2E_PACK_RESULTS` | off; set to `1` in the shipped file | the run |
| `E2E_OWN_TOOLING` | off | the run |
| `E2E_OPTIONS` | none: anything else for the runner's command line | the run |
| `E2E_TESTS` | none: the whole default test set | the run |

Three more are paths rather than policy, and belong in the environment or the file
just the same: `E2E_CRON_ENV`, the settings file itself, which only the environment
can say; `E2E_ORCHESTRATOR`, the binary, defaulting to the `build/bin` of the
deployed tree; and `E2E_RUNNER`, the runner the run hook invokes, defaulting to the
`e2e-runner` two directories up from the hook. The last is the one to reach for when
testing a hook against something which is not a two-hour test run.

Five settings are not commented out in the shipped file. Four of them are the ones
nothing downstream will guess at: `E2E_ROOT` has nowhere to default to, a wrong
guess there being a queue nobody is servicing, and the run hook will not invent a
repository, a branch or somewhere to publish -- polling defaults the first two and
puts them on the request it submits, but a request which carries neither, the
nightly one, has only this file to go on, and a job which cannot say what it is
testing fails rather than tests the wrong thing. The fifth, `E2E_PACK_RESULTS`, does
have a default and is set anyway: it defaults to off, `e2e-cron-job` used to default
it on, and shipping it set is what keeps a migrated host publishing archives instead
of quietly reverting to plain files.

Both this file and `E2E_PROXIES` are sourced, which is to say executed. Keep the
proxies file to proxy settings: a stray `E2E_REMOTE` in it wins over the settings
file, and the only sign is the wrong repository being polled.

The one variable in it which could do worse is `E2E_ORCHESTRATOR`, that value being the
program which then gets run: with a request's arguments on its command line in the
polling hook, and as the whole of what `e2e-cron-job` `exec`s. So both of them note what
they were given *before* sourcing the proxies file, refuse to run anything else
afterwards, and name the file in the message. Between them that catches a typo and a
substitution alike.

Checking merely that the value is executable would catch only the typo. `/bin/true`
passes such a check, and something like `/bin/true` is the shape this goes wrong in
rather than a contrived one: it exits zero and prints nothing, so the polling hook reads
it as a request already asked for and goes quiet, while `e2e-cron-job`'s `exec` becomes
a tick which polls nothing, sweeps nothing and admits nothing and still succeeds. Both
are silent, both are permanent, and both look exactly like a healthy quiet branch.

**Neither is a security check, and nothing should be built on them as if they were.** A
sourced file is executed, so whoever can write the proxies file already runs as the test
user and can do anything at all; what these two catch is an accident, a stray assignment
or settings pasted into the wrong file. What they do not catch is everything else a
sourced file can do, which is everything.

### Where the output goes

Three places, and they nest. What a tick prints goes wherever the crontab line
sends it: the polling hook saying the branch moved or that a test of it is already
asked for, and the orchestrator saying which job it started. Nothing a job says
reaches there, the job's supervisor being detached with the job log as both its
streams, so the tick says the one thing an operator could not otherwise find out --
that there is no `run` hook, and so nothing a job could do. That is the line to look
for when the nightly has gone quiet. What a job's hooks print goes to
`$E2E_ROOT/jobs/<job>/log` while it runs and `$E2E_ROOT/done/<job>/log` afterwards,
beside the request it came from and its exit status. And once a run has a directory to
publish into, everything the runner prints goes to `e2e-runner.log.txt` there, which
is the log the report page shows. Run by hand from a terminal the runner prints to
both.

To be told when a run fails, look at the verdict rather than at the exit status:

```shell
results=/opt/e2e-test/nri-plugins/results          # E2E_RESULTS
read -r verdict _ < "$results/latest/status.txt"
[ "$verdict" = PASS ] || echo "e2e run $verdict, see $results/latest/"
```

On a host behind a proxy, put the proxy variables in a file and point
`E2E_PROXIES` at it: `e2e-cron-job` exports them for everything a tick starts, and
the runner passes them into the test VMs.

### Three things to watch

The copy of `e2e-runner` which parses its options is the one in the tree
`e2e-cron-job` was installed from, `50-e2e-tests` invoking its sibling. The runner
then re-executes itself out of the worktree it makes, so the tests, the framework
and the report tool are always the branch's -- but it cannot re-execute its way out
of not understanding an option the copy which was invoked has never heard of.

So keep that tree up to date, and keep it apart from `E2E_CLONE`: a `git pull` and a
`make e2e-orchestrator` in the tree you deployed, and it is yours to do. Both, not
just the pull -- the orchestrator is a built binary in that tree's `build/bin`, and
a pull which leaves it alone leaves the one component whose contract the hooks are
written against at whatever revision it was last built from. `E2E_UPDATE_CLONE` went
with the rest of the runner's policy. There is nothing left for it to fix, and
resetting a clone which may be the very tree the hooks are running out of was never
something to reach for.

The handover cuts the other way for retention. A branch older than this tooling
still has a runner which prunes and publishes `latest` inside the run, with its own
defaults of 100 days and three runs kept, and the re-exec no longer carries any
retention setting across, those not being the runner's settings any more. So a host
configured to keep everything can still lose old results the moment it tests such a
branch. Test one with `E2E_OWN_TOOLING=1` if the published results matter: that
keeps our runner and never hands over.

The first run needs this script from somewhere, so start from a clone of your own
or from `E2E_CLONE`; from then on each run tests, and reports with, the revision it
fetched.

### Migrating a host from the older crontab

- **Delete the old lines.** The block above appends to `crontab -l`, so following it
  leaves the pre-orchestrator lines in place beside the new ones: the old `*/5` line
  logs its diagnostic every five minutes, the old nightly `e2e-cron-job
  --force-after 0` fails with `no such command "--force-after"` and mails that every
  night, and the docker prune ends up in there twice. `crontab -e` and take them out.
- Both crontab lines become `e2e-cron-job` with an argument, `tick` or `request`.
  A line with no argument at all says so and exits rather than running anything.
- **Remove any `E2E_CRON_ENV=` line from the crontab.** The older instructions put
  one there, so a host set up from them has one, and a crontab environment line
  applies to every command in the file -- the new lines included. Cron does not
  expand variables in one, so the value is those literal characters, the settings
  file installed at `/etc/sysconfig/nri-plugins-e2e-cron-job` is never read, and the
  first thing said about it is `no E2E_ROOT, so there is nowhere to keep jobs`, which
  points nowhere near the cause.
- **The nightly line and `E2E_FORCE_AFTER` no longer share a clock**, so a quiet
  branch now gets both a nightly run and a forced poll run: two multi-hour runs a day
  where there was one. Set `E2E_FORCE_AFTER=never` and keep the nightly line, or drop
  the nightly line and leave force-after alone, per the note beside the crontab
  block above.
- **Runner options are no longer ours to pass on.** Arguments to `e2e-cron-job` are
  the orchestrator's now, so the old one-off form, `e2e-cron-job --runtime crio`, is
  gone: put such things in `E2E_OPTIONS` or `E2E_TESTS`, where the run hook picks
  them up, or run `e2e-runner` directly for a one-off which should not be a job.
- **`E2E_RUN_IF_CHANGED` has no successor, and is silently ignored if left here.**
  Polling only ever asks for a test of a branch which moved, so there is nothing
  left to switch on. What it was used for the other way round -- emptied, to test
  on every trigger -- is now `E2E_FORCE_AFTER=0`. A host which leaves the old
  setting in place and changes nothing else gets one run a day instead of one per
  trigger, and nothing says so.
- `E2E_FORCE_AFTER` keeps its name, its spellings and its meaning, and moves from
  the runner to the polling hook.
- `--retention-days` and `--retention-keep` are gone from the runner and are
  `E2E_RETENTION_DAYS` and `E2E_RETENTION_KEEP` in the settings file. **The number
  of runs kept whatever their age changes from 3 to 10**, which is three times the
  directories under the result root; `E2E_RETENTION_KEEP=3` keeps the old figure.
- `E2E_UPDATE_CLONE` is gone, see above.
- `E2E_ROOT` is new and has to be set, and `E2E_REMOTE`, `E2E_BRANCH` and
  `E2E_RESULTS` now want setting rather than defaulting, per the note under the
  table above.
- **`E2E_PACK_RESULTS` no longer defaults to on.** `e2e-cron-job` used to default
  it; nothing does now, so a host which had it commented out and relied on that
  starts publishing plain files instead of an archive.
- Anything in `E2E_OPTIONS` which was `--run-if-changed`, `--force-after`,
  `--retention-days` or `--retention-keep` now fails the run with `unknown command
  line option`.

## Testing a branch with your own tooling

A run hands over to the tested revision: it adds the worktree, then re-executes
the `e2e-runner` it finds there and builds `e2e-report` out of it, so a run is
driven and reported on by the revision under test. That is what you want when the
revision is what you are testing.

It is the wrong way round when the *tooling* is what you are testing. Changes to
the runner or to `e2e-report` have nowhere to be exercised: putting them on a
branch and testing that branch tests them against whatever else is on it, and
testing `main` throws them away at the handover. `--own-tooling` keeps them:

```shell
scripts/testing/nightly/e2e-runner --branch main --own-tooling \
    --results /opt/e2e-test/nri-plugins/results
```

The worktree is still added at the branch, and the tests and the plugins are
still the branch's -- only the tooling is ours. So the line above runs `main`'s
tests against `main`'s plugins, with the runner and the reporter of the tree it
was started from. `E2E_OWN_TOOLING=1` in the `e2e-cron-job` settings does the same
from cron.

Both tools or neither, deliberately: it is the runner which records what a report
reads, so a run driven by one revision's runner and reported on by another's
loses whatever the two do not agree about. Asking for it from a tree which has no
`test/e2e/cmd/e2e-report` in it is refused before the run rather than after the
tests, when there would be nothing left to report them with.

The alternative is to keep a tooling branch rebased on `main` and test that,
which works as long as the branch touches nothing outside the tooling --
`git diff --name-only main..<branch>` says whether it does. `--own-tooling` is
what saves the rebasing.

## Serving the results

`e2e-report serve` is what serves the results, and the only thing which can: the
index of the runs and the report of each run are rendered for every request, not
written out, so there are no pages under the result root for a file server to
serve.

```shell
make e2e-report
install -m 755 build/bin/e2e-report /usr/local/bin/
e2e-report serve --address :8080 /opt/e2e-test/nri-plugins/results
```

It serves a packed run as if its archive had been extracted, serves into the
tarballs of the tests as well, and reads nothing but what is under the result
root. It writes nothing at all, so it is safe for a server given the results
read-only.

Rendering every page as it is asked for is what keeps the results in step with
the tool serving them: a run published months ago is shown the way a run
published today is, without anything being migrated or rewritten, and a run still
going is listed and reported on from what it has collected so far.

What no amount of rendering can show is something a run never recorded. A run
pruned by an older runner recorded no link to its plugin log, and only reading its
results again finds one inside `artifacts.tar.xz`:

```shell
e2e-report refresh /opt/e2e-test/nri-plugins/results
```

That reports on every unpacked run under the root again. Packed runs are left
alone, their results being inside the archive.

There is a systemd unit for it next to this file:

```shell
install -m 644 scripts/testing/nightly/nri-plugins-e2e-results.service \
    /etc/systemd/system/nri-plugins-e2e-results.service
install -m 644 scripts/testing/nightly/nri-plugins-e2e-results.env \
    /etc/sysconfig/nri-plugins-e2e-results
systemctl daemon-reload
systemctl enable --now nri-plugins-e2e-results
```

The address to listen on and the result root come from
`/etc/sysconfig/nri-plugins-e2e-results`, so a host is configured without editing
the unit; the user and the group are in the unit itself, as systemd does not
expand variables there. The `.env` file is optional, and so is every setting in
it: what it leaves out the unit defaults to.

For a name and a certificate, put caddy in front of it with the configuration
next to this file, which does nothing but pass everything on:

```shell
E2E_PORT=8443 E2E_SERVER=127.0.0.1:8080 caddy run \
    --config scripts/testing/nightly/nri-plugins-e2e-results.Caddyfile
```

Keep the server's own address on the loopback interface as it comes: that is the
only place the restriction lives, and caddy binds every interface on purpose.

`make e2e-report` builds it to `build/bin`, and nothing else does: it is not part
of any image and not one of the binaries a release ships. Build it again when the
results it serves start coming from a newer revision. `go run
./test/e2e/cmd/e2e-report serve ...` from a checkout works just as well.

## Packing up a run

A run of the whole suite collects some 120M and publishes 20M of it, having
pruned and packed the rest away. `--pack-results` publishes all of it in a
single `results.tar.zst` of some 2.5M instead, keeping the artifacts of every
test and the coverage data of each:

`E2E_PACK_RESULTS=1` in the settings file, which the shipped one sets, or
`--pack-results` on the runner:

```shell
scripts/testing/nightly/e2e-runner --results /opt/e2e-test/nri-plugins/results \
    --pack-results
```

`results.json`, `status.txt` and `summary.txt` stay where they are, so how a run
went is readable without unpacking anything and its report renders in full. The
logs, the command transcripts and the coverage report are all in the archive.
That takes `e2e-report serve`, so set that up first. `tar --zstd -xf` gets the
results of a run out without a server.

Packing is the last thing a run does, after the verdict, because the log of the
runner is packed with the rest and anything written to it afterwards would go
nowhere. So the log in the archive is the whole of it, down to the verdict; the
only thing missing is what packing itself reports. That is also why the runner
builds `e2e-report` out of the worktree into a directory of its own rather than
running it with `go run`: by the time it packs, the worktree is gone.

## What a run publishes

```text
<result root>/latest -> <newest run>
              <run>/results.json            what the run collected, which its
                                            report is rendered from
                    status.txt              PASS 55/55 tests passed
                    summary.txt             a line per test case
                    git.describe, git.sha1, git.remote, git.branch
                    git.runner              the tree the runner came from
                    e2e-runner.log.txt      the log of the run
                    coverage-report/        profile, browsable report, summary
                    <topology-distro-runtime>/policies.test-suite/<policy>/<test>/
                    results.tar.zst         all of the above, with --pack-results
```

A run records where it came from as well as how it went: `git.remote` and
`git.branch` are the repository and the branch it was picked up from,
`git.describe` and `git.sha1` the revision it tested of them, and `git.runner`
the tree the runner script itself came from. That last one is normally the
revision under test, since the runner re-execs itself out of the worktree it
creates, so a report mentions it only when the two differ, which happens for a
run driven by hand or with `--skip-worktree`. It is left out altogether when
there is no telling, as for a runner read straight out of a repository with
`git show`.
