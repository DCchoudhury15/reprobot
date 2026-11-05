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
5. **Analyze.** Parse sanitizer output (ASan, UBSan). Build a crash
   signature from the crash kind and the top 3 frames that belong to the
   project (skip sanitizer runtime and libc frames). Compare with the claim.
6. **Dedup.** Look the signature up for the project. Same signature means
   same bug.

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

## Sandbox

v0 uses Docker. It uses the gVisor runtime (`runsc`) when it is installed and
falls back to `runc` with hardening flags when it is not. Firecracker on bare
metal comes later. Workers hold no secrets: inputs go in, results come out.

## How we know it works

For a real bug, the same reproducer must give `REPRODUCED_AS_CLAIMED` on the
vulnerable commit and `NOT_REPRODUCED` on the fix commit. If the fix is
reverted, it must flip back. Every test fixture follows this rule.

## Later

Postgres job queue with leases, intake API, result page, GitHub App for
private vulnerability reports, OSS-Fuzz project import, fix verification,
bisect, signed results, Firecracker.
