# Evaluation on real bugs

The tests in `internal/` use a small fixture program. This folder checks
reprobot against real, fixed security bugs in real projects.

## The rule

For every real bug, the same report is run twice:

1. on the last vulnerable commit, where it must give `REPRODUCED_AS_CLAIMED`;
2. on the fix commit, where it must give `NOT_REPRODUCED`.

Only the commit changes between the two runs. A report about a bug that does
not exist must give `NOT_REPRODUCED`.

## Run it

```
go test -tags eval -timeout 60m -v ./eval/
```

Needs Docker, git and network access to clone the project. Useful settings:

- `REPROBOT_EVAL_CACHE=DIR` keeps the git mirror and the builds between runs.
- `REPROBOT_EVAL_WRITE=1` rewrites `RESULTS.md` when every case passes.
- `REPROBOT_EVAL_SITE=1` writes `site.json` next to `RESULTS.md` when every
  case passes, and rebuilds `site/real.js` (the recorded runs the project site
  shows) from the `site.json` of each project the site lists.

## libxml2

Results: [libxml2/RESULTS.md](libxml2/RESULTS.md). Five CVEs, all reproduced
on the vulnerable commit and all gone on the fix commit. Three false reports,
none reproduced. A cold run (clone plus nine builds) takes about four to six
minutes on a laptop; one build takes 25 to 35 seconds and a cached check 1 to
2 seconds.

Each case is a folder in `libxml2/` with a `repro.yaml` and its input files.
`libxml2/cases.yaml` lists the fix commit of each bug and where its input
comes from. Four inputs are the regression tests that the libxml2 maintainers
added in the fix commit (libxml2 is under the MIT license). The input for
CVE-2025-24928 was written for this evaluation from the fix diff.

The three false reports were also written for this evaluation:

- a harmless long text node, with a claimed heap overflow in a real function;
- a claimed overflow in a function that does not exist, which the static
  claim check also points out;
- an input file that is a copy of a sanitizer report. `xmllint` prints the bad
  line in its error message, so the report text appears on stderr. reprobot
  does not read crashes from stderr, so this does not count.

## jq

Results: [jq/RESULTS.md](jq/RESULTS.md). Two CVEs from the jq 1.7.1 security
release, both reproduced on the vulnerable commit and both gone on the fix
commit. Two false reports, none reproduced. Each input is the regression test
the jq maintainers added to `tests/shtest` in the fix commit:

- CVE-2023-50246: `jq .` on the number `-10E-1000000001` overflows a heap
  buffer while printing it.
- CVE-2023-50268: `jq '1 != .'` on `Nan4000` (a NaN with a large payload)
  overflows a stack buffer while comparing.

Both crashes happen in the decNumber library that jq bundles
(`decToString`, `decNumberCopy`); the jq functions named in the reports are
their callers in the top frames, so the claims match. The vulnerable commit
of CVE-2023-50246 is the fix commit of CVE-2023-50268, so the two cases do
not mix.

Two build notes: jq from 2023 needs `-std=gnu17`, because GCC 15 defaults
to C23, where `f()` means `f(void)`; and it needs a full `make`, because
`make jq` skips generated sources such as `src/builtin.inc`.

jq also fixed many memory bugs in 2026, but those fixes add no input-level
regression tests, so they are not used here.

## What this found

- CVE-2025-49795 and CVE-2025-49796 are two different bugs, but both are a
  SEGV with the same top three frames, so they get the same signature. They
  crash on different lines of the same function: `switch (eval->type) {` and
  `if ((node->ns == NULL) || ...)`. The signature does not use line numbers
  because they change between commits, so it first grouped these two as
  duplicates. reprobot now also records the crash line (the function plus
  the source text of the line, read from the commit), and lists reports with
  the same signature but a different line as "same stack, different line"
  instead. The eval fails if two different bugs would be listed as
  duplicates.
- The titles of CVE-2025-49794 and CVE-2025-49796 name
  `xmlSchematronReportOutput`. In the real stack the crash is in
  `xmlSchematronFormatReport`, and the named function is not in the top
  frames. A report that copies the function from the CVE title gets
  `REPRODUCED_DIFFERENT`, not a rejection.
- With `--noout`, `xmllint` runs schematron checks in quiet mode and the bugs
  in the report text code are not reached. The command line in a report
  matters as much as the input file.

## PCRE2

Results: [pcre2/RESULTS.md](pcre2/RESULTS.md). One CVE, reproduced on the
vulnerable commit and gone on the fix commit. Two false reports, none
reproduced. The input is the regression test the PCRE2 maintainers added to
`testdata/testinput6` in the fix commit, run with PCRE2's own test program
`pcre2test`:

- CVE-2026-86145 (GHSA-3r4p-g7gg-ppmf): DFA matching of a recursive pattern
  under a 4 KiB heap limit writes past the end of the workspace. The
  advisory names `pcre2_dfa_match()`; the write happens in
  `internal_dfa_match`, the function it calls in the same file, so that is
  the claimed frame.

The false reports:

- the same pattern without the heap limit, claiming the same overflow;
- a claimed overflow in a function that does not exist, which the static
  claim check also points out.

Another PCRE2 bug was tried first and left out: the regression test for
CVE-2025-58050 makes the vulnerable commit print a wrong result ("No match"),
but it does not read out of bounds, so there is no sanitizer crash to check.

## Limits

- Four projects, nine bugs, all through one command line program each
  (`xmllint`, `jq`, `curl` and `pcre2test`).
- The false reports are written by hand. They are not real reports received
  by a project.
- Bugs that need a network peer use scripted loopback servers
  (`run.servers`). The curl case is the only one so far.

## curl

Results: [curl/RESULTS.md](curl/RESULTS.md). One CVE, reproduced on the
vulnerable commit and gone on the fix commit. Two false reports, none
reproduced. The bug needs a network peer, so the report runs a scripted HTTP
proxy on loopback (`run.servers`).

- CVE-2022-42915: when an HTTP proxy refuses the `CONNECT` for a `dict`,
  `gopher` or `telnet` URL, curl's error cleanup frees the same memory twice.
  The command line and the proxy's 503 reply are curl's own regression test
  for the fix, `tests/data/test445` (added in `038bfb8`, right after the fix
  `55e1875`). The report claims the kind (double free) but no function, as
  the public advisory names none.

The false reports:

- the same refused proxy with an `http` URL, which the advisory says was
  never affected, claiming the same double free;
- a SOCKS5 redirect to a 200 byte hostname, claiming the CVE-2023-38545 heap
  overflow.

### Why not CVE-2023-38545

The first curl case was CVE-2023-38545, the SOCKS5 hostname overflow. It never
reproduced, so it was replaced. The overflow needs a slow SOCKS5 handshake and
a hostname longer than curl's download buffer (about 100 KB in the curl tool
unless `--limit-rate` makes it smaller). curl's regression test for that fix
(`tests/data/test728`) only checks that the fixed curl refuses a 256 byte
hostname with error 97. On the vulnerable commit the same input makes curl go
on without overflowing anything, so it cannot give a sanitizer crash.
