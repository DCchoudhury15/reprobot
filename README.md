# reprobot

[![ci](https://github.com/DCchoudhury15/reprobot/actions/workflows/ci.yml/badge.svg)](https://github.com/DCchoudhury15/reprobot/actions/workflows/ci.yml)

reprobot checks security reports by running them.

Open source projects now get more security reports than they can read, and
many are automated reports about bugs that do not exist. A report that reads
well and a real bug look the same on paper. The only reliable test is whether
the crash actually happens.

reprobot takes a small `repro.yaml` from the reporter, builds the exact commit
with sanitizers using a build profile the maintainers control, runs the proof
of concept in a sandbox with no network, and records what really happened.
Reports that crash the same way are grouped together.

```
$ reprobot run --project project.yaml repro.yaml
state:     REPRODUCED_AS_CLAIMED
reason:    crashed in 3 of 3 runs, as claimed

crash:     address heap-buffer-overflow, WRITE of size 64
frames:
  #1 parse_header /src/tinyparse.c:21
  #2 parse_file /src/tinyparse.c:35
  #3 main /src/tinyparse.c:46
signature: d3c66c5eb2dafff2
```

The same reproducer on the fix commit gives `NOT_REPRODUCED`.

## Verdicts

| State | Meaning |
|---|---|
| `REPRODUCED_AS_CLAIMED` | crashed with the claimed kind and frame |
| `REPRODUCED_DIFFERENT` | crashed, but not as the report says |
| `NOT_REPRODUCED` | no run crashed |
| `FLAKY` | some runs crashed |
| `TIMEOUT`, `OOM` | hit a limit; may be a real hang, never called fake |
| `BUILD_FAILED`, `INVALID_INPUT` | the input could not be tested |
| `OUT_OF_SCOPE` | the claim is not a crash, so running it cannot test it |

## Why a pasted report cannot fake a crash

The reporter controls the input, and many programs print bad input in their
error messages. So reprobot never reads crashes from stdout or stderr. The
command must be a program the maintainers build, and a crash counts only when
the sanitizer writes its report into a secret per-run log directory and the
program exits with a secret per-run exit code. Details in
[docs/DESIGN.md](docs/DESIGN.md).

## Quick start

Needs Go 1.26, git and Docker.

```
go install github.com/DCchoudhury15/reprobot/cmd/reprobot@latest
reprobot run --project project.yaml path/to/repro.yaml
```

See `testdata/fixtures/tinyparse` for a complete example project and
reproducer. The service (intake API, Postgres job queue, workers) is described
in [docs/SERVICE.md](docs/SERVICE.md).

## Tests

```
go test ./...                           # unit and database tests (Docker)
go test -tags e2e ./internal/runner/    # real sanitizer builds in the sandbox
```

## Status

Early. Working: the CLI, sandboxed builds and runs, ASan and UBSan parsing,
crash signatures, the static claim check, the job queue, intake API and
result page. Not yet: OSS-Fuzz project import, a GitHub App for private
vulnerability reports, signed results, gVisor by default, and auth on the API.

## License

Apache License 2.0. See [LICENSE](LICENSE).
