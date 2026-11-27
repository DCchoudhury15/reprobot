# reprobot design

reprobot checks security reports by running them. A reporter sends a small
file that says which commit to build and how to trigger the bug. reprobot
builds that commit with sanitizers, runs the proof of concept in a sandbox,
and writes down what really happened. Maintainers read the result before they
read the report.

Two rules drive everything:

1. Trust only what happens when the code runs, never the text of the report.
2. Never run reporter input outside a sandbox.

No language model makes any decision in this system.

## Inputs

### Reporter file: `repro.yaml`

```yaml
version: 1
project: tinyparse          # id of a project known to reprobot
commit: 3f2a9c1             # exact commit or tag
profile: asan               # name of a build profile; maintainers define it
run:
  cmd: ["bin/tinyparse", "poc/input.bin"]
  timeout_seconds: 30       # per run, default 30, max 300
  repeat: 5                 # how many times to run, default 5, max 20
files:                      # paths relative to repro.yaml, mounted read-only
  - input.bin
claim:
  sanitizer: address        # address | undefined | none
  kind: heap-buffer-overflow
  frame: parse_header       # optional: function the reporter says crashes
  text: |                   # optional: the free text of the report
    ...
```

Paths in `run.cmd` are relative to the run directory inside the sandbox:
`bin/` holds the build outputs, `poc/` holds the reporter files.
`run.cmd[0]` must be one of the profile outputs (`bin/<output>`). A shell or
any other program from the image is rejected, because it would let the
reporter print anything.

### Maintainer file: project config

Build profiles belong to the maintainers. The reporter only picks a profile
by name, so a reporter cannot change build flags or patch the source.

```yaml
version: 1
id: tinyparse
repo: https://example.org/tinyparse.git   # git URL or local path
image: gcc:15                             # build and run image
profiles:
  asan:
    build: make CFLAGS="-fsanitize=address -g -O1 -fno-omit-frame-pointer"
    outputs: [tinyparse]                  # copied into bin/
    env:
      ASAN_OPTIONS: detect_leaks=0:symbolize=1
limits:
  cpus: 2
  memory_mb: 2048
  build_timeout_seconds: 900
```

## Pipeline

1. **Intake.** Validate `repro.yaml`, store it, queue a job. The job key is
   the report id plus a hash of the reproducer, so a retried submit does not
   create a second job.
2. **Claim check.** Cheap, no build. Look for function names, file paths and
   `file:line` references in the claim and check that they exist at that
   commit with `git grep`. A report that cites code that does not exist is
   flagged right away.
3. **Build.** Cache key = sha256 of project id, commit, profile name, profile
   content and image digest. On a hit, reuse the outputs. On a miss, build in
   a sandbox with no network. The source is fetched before the build starts.
4. **Run.** Every run gets a fresh sandbox: no network, read-only root,
   tmpfs for scratch, CPU, memory, pid and time limits, all capabilities
   dropped, no new privileges. Run `repeat` times.
5. **Analyze.** Parse the sanitizer report (ASan, UBSan) from the report
   channel described below, never from stdout or stderr. Build a crash
   signature from the crash kind and the top 3 frames that belong to the
   project (skip sanitizer runtime and libc frames). Compare with the claim.
6. **Dedup.** Look the signature up for the project. Same signature means
   same bug, with one refinement: the runner also reads the source line of
   the top project frame at that commit and hashes the function plus that
   line's text (whitespace collapsed) into a crash site. Two reports with
   the same signature but different sites crash on different lines of the
   same function, so they are listed as related (same stack, different
   line), not as duplicates. A report without a site (the line could not be
   read) matches either way. The line text, not the line number, is used so
   the same bug at another commit keeps its site.

## Result states

| State | Meaning |
|---|---|
| `REPRODUCED_AS_CLAIMED` | crashed with the claimed kind (and frame, if given) |
| `REPRODUCED_DIFFERENT` | crashed, but not as the claim says |
| `NOT_REPRODUCED` | no run crashed |
| `FLAKY` | some runs crashed, some did not |
| `TIMEOUT` | runs hit the time limit; may be a real hang bug, not called fake |
| `OOM` | runs hit the memory limit; same note as timeout |
| `BUILD_FAILED` | the profile did not build at that commit |
| `INVALID_INPUT` | bad file, unknown project or profile, missing commit |
| `OUT_OF_SCOPE` | the claim is not a crash (claim.sanitizer is `none`) |

A real logic bug must never be labelled fake. reprobot only judges claims it
can test.

## Fake reports

The reporter controls the input. If the program echoes its input (many
parsers print bad input in error messages), a reporter could paste a real
sanitizer report into the input and make it appear on stderr. So reprobot
never reads crashes from stdout or stderr. Each run gets:

- a fresh out dir mounted at `/work/out`, mode 0711, so the run user can
  enter it but not list it;
- inside it, a subdir with a random 128-bit name, mode 2733 (anyone may
  create files, only reprobot may list; setgid so reprobot can read files
  the run user owns);
- `log_path` pointing the sanitizer into that subdir and a random
  sanitizer `exitcode` (64 to 120), both appended last to `ASAN_OPTIONS`
  and `UBSAN_OPTIONS` so they win over the profile.

A crash counts only when a report file exists in the secret subdir and the
exit code equals the secret one. Log files are opened without following
symlinks and without blocking on FIFOs, because the run user owns them.

### The crash check from outside the process

The secret log dir and exit code reach the sanitizer through the
environment, so the program under test can read them too. That does not
matter for programs that only parse the reporter's input (`xmllint`, image
decoders, most fuzz targets): the input cannot run code, it can only be
echoed. It does matter when the input is a program, such as a script for an
interpreter: the script can read the options, write a fake report into the
secret dir and exit with the secret code.

So every run starts under `reprobot-watch` (`cmd/reprobot-watch`,
`internal/watch`), a small static binary mounted read-only into the run
container as PID 1. It starts the command as a ptrace child and stops each
thread of the program as it exits. A sanitizer ends a process from its own
runtime: `__sanitizer::internal__exit` calls `exit_group` with the exitcode.
At the exit stop the watcher reads the registers and the memory map and
checks:

- the call is `exit_group`, from a thread of the program, with the secret
  exit code;
- the instruction is in a shared sanitizer runtime (`libasan.so`,
  `libubsan.so`, `libclang_rt.asan-*.so`, ...), or in a function of the
  `__sanitizer` namespace inside the program (a static runtime, found by
  its ELF symbol);
- the code is not mapped from a place the program can write to (`/tmp`,
  `/dev/shm`, the out dir, the reporter files) or from a deleted file.

Once the program has exited the watcher kills every process left, then
writes its verdict (`watch.json`: pid, exit status, whether the exit came
from the sanitizer, and why) into the secret dir, removing any file the
program planted under that name. A crash then also needs the verdict to say
it came from the sanitizer, and the report must be in `asan.<pid>` or
`ubsan.<pid>` for the program's own pid. With no verdict (the run was
killed at the time limit) a report is not counted. If the watcher itself
fails (ptrace not allowed) the run is an infrastructure error, not a
verdict.

The program cannot change the verdict: it cannot kill PID 1 of its pid
namespace (the kernel drops such signals, and the watcher takes every
catchable one), it cannot read or write the watcher's memory (the watcher
makes itself non-dumpable), and the verdict is written only when no other
process is left. Before the watcher, a script that forged a report gave
`REPRODUCED_AS_CLAIMED` on a commit with no bug; `TestE2EScriptForgedReportIsCaught`
now gives `NOT_REPRODUCED`, with the note "exit_group was called from _exit
in .../libc.so.6".

### What this does not cover

A program that can run native code (an FFI, loading a shared library it
wrote) can call the sanitizer's own report function and get a real report
with a real exit from the runtime. No check, inside or outside the process,
can tell that from a real crash, because it is one. So profiles whose input
can run native code are still not supported, and a maintainer should not
add one.

Hiding the options after start-up was also tried (a preloaded library that
removes them from the environment once the sanitizer has read them). It
breaks UBSan, which reads its options at the first report, so the report
and exit code are lost.

The watcher reads registers for linux/amd64 only. On other platforms it
reports an error and the run fails rather than giving a verdict. It has not
been tried under gVisor (`runsc`), which implements ptrace itself.

## Sandbox

v0 uses Docker. It uses the gVisor runtime (`runsc`) when it is installed and
falls back to `runc` with hardening flags when it is not. Firecracker on bare
metal comes later.

Workers hold one secret at most: the signing key, when they run with
`--sign-key`. Code that escapes the sandbox onto a worker host could read
that key and sign false verdicts. So keep the key only on worker hosts, give
each deployment its own key, and make a new key (and publish the new public
key) if a worker host may have been reached. Without `--sign-key`, workers
hold no secrets: inputs go in, results come out.

## How we know it works

For a real bug, the same reproducer must give `REPRODUCED_AS_CLAIMED` on the
vulnerable commit and `NOT_REPRODUCED` on the fix commit. If the fix is
reverted, it must flip back. Every test fixture follows this rule, and so
does the real CVE evaluation in `eval/` (libxml2, jq and curl).

## Fix check

`reprobot run --fix COMMIT` runs the same report file twice: on the commit
in the report, then on COMMIT. Only the commit changes, so the reporter
files, the command and the reproducer hash stay the same. If the first run
does not reproduce (all runs crashed), the fix commit is not built, because
there is nothing to check. The verdict is `FIXED` (crashed, then no run
crashed), `NOT_FIXED` (still crashes in some or all runs, with the same or a
different crash) or `CANNOT_TELL` (the fix commit gave `BUILD_FAILED`,
`TIMEOUT`, `OOM` or `INVALID_INPUT`). A `FLAKY` report commit is
`CANNOT_TELL` too: a bug that crashes only sometimes cannot show that a fix
works.

In the service a report can carry a `fix_commit`. The worker runs both
commits in one job, under one lease, and stores the fix check next to the
result. If the fix run hits an infrastructure error the whole job is
retried; the report run is then a build cache hit. A signed fix check has
its own payload type (`application/vnd.reprobot.fix+json`), and the type is
part of what is signed, so a signed fix check can never pass as a signed
result or the other way round. It holds both runs, so it can be checked on
its own.

## Bisect

`reprobot run --bisect GOOD` finds the first commit with the crash. It
runs the report on its own commit first; only a report that reproduced
there (every run crashed) is bisected. Then it walks the first-parent
history from the report commit back to GOOD. That is the main line of a
project that merges branches, so each commit in the list is the child of
the one before it. GOOD must be on it: a commit on a merged branch, a
later commit or an unrelated one gives `CANNOT_TELL` and runs nothing. A
merge that brings the bug in is named as the merge commit.

GOOD is run next, since the answer is only right if GOOD has no crash. If
it has the same crash, or gives no answer, the bisect stops with
`CANNOT_TELL`. Then it halves the range. Each commit is run like any
report, with the same files, command and number of runs, and gets a
verdict:

- **good**: `NOT_REPRODUCED`.
- **bad**: it crashed with the same bug as on the report commit: the same
  signature in any run, or the same crash line. A bisect looks for this
  bug, not for any crash; counting another crash as bad would name the
  commit that added the other bug. The crash line covers a rename of a
  function further up the stack.
- **skip**: anything else (`BUILD_FAILED`, `TIMEOUT`, `OOM`, another
  crash). The next commit tried is the untested one nearest the middle.

`FOUND` names the first bad commit and its parent, which is good. `RANGE`
means skips are left between the last good and the first bad commit, so it
is one of those. A bisect stops after 24 runs past GOOD (a long stretch of
commits that do not build) with `RANGE` too.

In the service a report can carry a `good_commit`. The bisect runs in the
same job as the report, and a signed bisect has its own payload type
(`application/vnd.reprobot.bisect+json`).

## Later

OSS-Fuzz project import, Firecracker.
Built since this list was first written: the Postgres job queue with
leases, the intake API with tokens and rate limits, the result page, signed
results, the fix check in the CLI (`run --fix`) and the service
(`fix_commit`), bisect (`run --bisect`, `good_commit`), the crash check from outside the process
(`reprobot-watch`), and the GitHub App intake for private vulnerability
reports (see SERVICE.md).
