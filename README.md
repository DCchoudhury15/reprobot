# reprobot

[![ci](https://github.com/DCchoudhury15/reprobot/actions/workflows/ci.yml/badge.svg)](https://github.com/DCchoudhury15/reprobot/actions/workflows/ci.yml)

**reprobot checks a security report by running it.** It builds the exact
commit with sanitizers, runs the reporter's proof of concept several times in
a sealed sandbox, and tells you whether it really crashed.

Site: https://reprobot-two.vercel.app

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

The same report on the fix commit gives `NOT_REPRODUCED`.

## Why

Open source projects get more security reports than they can read, and many
are generated reports about bugs that do not exist. On paper, a well written
fake and a real bug look the same. Whether the crash actually happens is the
one thing a report cannot fake, so reprobot checks that and nothing else.

## How it works

1. **The reporter sends a `repro.yaml`:** the commit, the command to run, the
   input files, and what they claim crashes.
2. **reprobot builds that commit** with a build profile the maintainers own.
   The reporter picks a profile by name and cannot change any build flag.
3. **It runs the command several times** (`repeat`, 5 by default) in a
   container with no network, a read-only filesystem, no Linux capabilities,
   and CPU, memory and time limits.
4. **It reads the sanitizer report**, compares the crash to the claim, gives a
   verdict, and groups reports that crash the same way.

What a reporter sends (a real libxml2 CVE from [`eval/`](eval/)):

```yaml
version: 1
project: libxml2
commit: cb1b8b8516ade9add9f63fa0e39eaa3bc7034828
profile: asan
run:
  cmd: ["bin/xmllint", "--noout", "--schema", "poc/poc.xsd", "poc/poc.xml"]
  repeat: 3
files: [poc.xsd, poc.xml]
claim:
  sanitizer: address
  kind: SEGV
  frame: xmlSchemaFixupComplexType
```

What the maintainers set once per project:

```yaml
version: 1
id: tinyparse
repo: https://example.org/tinyparse.git
image: gcc:15
profiles:
  asan:
    build: make CFLAGS="-fsanitize=address -g -O1 -fno-omit-frame-pointer"
    outputs: [tinyparse]
limits: {cpus: 1, memory_mb: 1024, build_timeout_seconds: 300}
```

A bug that needs a network peer can describe up to 4 scripted TCP servers on
loopback (`run.servers`): read a line, send a listed file, wait, close.

## Verdicts

| State | Meaning |
|---|---|
| `REPRODUCED_AS_CLAIMED` | crashed with the claimed kind and frame |
| `REPRODUCED_DIFFERENT` | crashed, but not the way the report says |
| `NOT_REPRODUCED` | no run crashed |
| `FLAKY` | some runs crashed, some did not |
| `TIMEOUT`, `OOM` | hit a limit; may be a real hang, never called fake |
| `BUILD_FAILED`, `INVALID_INPUT` | the input could not be tested |
| `OUT_OF_SCOPE` | the claim is not a crash, so running it cannot test it |

Each crash gets a **signature** (crash kind plus the top 3 project frames) and
a **crash line** (the function plus the source text of the line it crashed
on). Same signature and same line means a duplicate. Same signature on a
different line is listed as "same stack, different line", because that is
often a second bug.

## Why a report cannot fake a crash

Many programs print bad input back in their error messages, so a reporter
could make a fake sanitizer report appear on stderr. reprobot never reads
crashes from stdout or stderr. A crash counts only when:

- the command is a program the maintainers build, not a script from the report;
- the sanitizer writes its report into a log directory whose path is secret
  and new for every run;
- the program exits with an exit code that is also secret and new for every run.

Before anything is built, a static check also looks for the functions, files
and `file:line` references the report names in the code at that commit. Details in
[docs/DESIGN.md](docs/DESIGN.md).

## Tested on real bugs

[`eval/`](eval/) runs reprobot on five fixed libxml2 CVEs. Each report is run
on the last vulnerable commit and again on the fix commit:

| Bug | Vulnerable commit | Fix commit |
|---|---|---|
| CVE-2025-49794 | `REPRODUCED_AS_CLAIMED` | `NOT_REPRODUCED` |
| CVE-2025-49795 | `REPRODUCED_AS_CLAIMED` | `NOT_REPRODUCED` |
| CVE-2025-49796 | `REPRODUCED_AS_CLAIMED` | `NOT_REPRODUCED` |
| CVE-2025-24928 | `REPRODUCED_AS_CLAIMED` | `NOT_REPRODUCED` |
| CVE-2023-28484 | `REPRODUCED_AS_CLAIMED` | `NOT_REPRODUCED` |

Three reports about bugs that do not exist (one of them a pasted sanitizer
report that `xmllint` echoes to stderr) all give `NOT_REPRODUCED`. A build
takes about 30 seconds; a check on a cached build takes 1 to 2 seconds. Full
table: [eval/libxml2/RESULTS.md](eval/libxml2/RESULTS.md).

## Quick start

Needs Go 1.26, git and Docker.

```
go install github.com/DCchoudhury15/reprobot/cmd/reprobot@latest
reprobot run --project project.yaml path/to/repro.yaml
```

`testdata/fixtures/tinyparse` is a small complete example. Exit codes: `0`
reproduced as claimed, `1` any other verdict, `2` usage error, `3`
infrastructure error. Add `--json` for the full result.

## Run it as a service

`reprobot serve` is an HTTP intake API with result pages, and `reprobot worker`
runs the queued reports. Both share one Postgres database.

- With `--tokens`, every route needs an API token. A reporter token sees only
  its own reports; a maintainer token sees every report of its projects.
  Without tokens, `serve` stays on loopback unless told otherwise.
- Each token can send 20 reports an hour by default.
- Duplicate lists only show reports the caller is allowed to see.

Setup and API: [docs/SERVICE.md](docs/SERVICE.md).

## Signed results

A signed result lets anyone check a verdict without running it again, for
example when it is pasted into an advisory. The signature covers the verdict
and the exact commit, build image and reproducer it was tested with.

```
reprobot keygen ~/.config/reprobot        # signing.key (keep) and signing.pub (publish)
reprobot run --project project.yaml --sign-key ~/.config/reprobot/signing.key repro.yaml > result.dsse.json
reprobot verify --key signing.pub result.dsse.json
```

The file is a standard [DSSE](https://github.com/secure-systems-lab/dsse)
envelope signed with Ed25519, so other DSSE tools can check it too. Workers
can sign every result the service produces, see
[docs/SERVICE.md](docs/SERVICE.md#signed-results).

## Status

Early.

Working: the CLI, sandboxed builds and runs, ASan and UBSan, crash signatures
and crash lines, the static claim check, loopback test servers, the service
with tokens, per-token access and rate limits, and signed results.

Not yet: OSS-Fuzz project import, a GitHub App for private vulnerability
reports, and gVisor as the default sandbox.

## Development

```
go test ./...                           # unit and database tests (needs Docker)
go test -tags e2e ./internal/runner/    # real sanitizer builds in the sandbox
go test -tags eval -timeout 60m ./eval/ # the real CVE evaluation
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
