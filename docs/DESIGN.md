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

### What this does not cover

The secret log dir and exit code reach the sanitizer through the
environment, so the program under test can read them too. This is fine for
programs that only parse the reporter's input (`xmllint`, image decoders,
most fuzz targets): the input cannot run code, it can only be echoed. It
does not hold for targets where the input is a program, such as an
interpreter that can read files or call native code. A script there can
read the options, write a fake report into the secret dir and exit with
the secret code.

Hiding the options after start-up was tried (a preloaded library that
removes them from the environment once the sanitizer has read them):

- it works for ASan, which reads its options before any program code runs;
- it breaks UBSan, which reads its options at the first report, so the
  report and exit code are lost;
- it does not stop a script from reading `/proc/self/mem`, or from calling
  the sanitizer's own report function through an FFI, which gives a real
  report with real frames.

Any check that runs inside the same process can be copied by code running
in that process. So reprobot does not support profiles whose input is code
with file or native access, and a maintainer should not add one. Checking
such targets needs a check from outside the process (for example ptrace),
which is future work.

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
does the real CVE evaluation in `eval/` (libxml2 and jq).

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

## Later

GitHub App for private vulnerability reports, OSS-Fuzz project import,
bisect, an out-of-process crash check (see Fake reports), Firecracker.
Built since this list was first written: the Postgres job queue with
leases, the intake API with tokens and rate limits, the result page, signed
results, and the fix check in the CLI (`run --fix`) and the service
(`fix_commit`).
