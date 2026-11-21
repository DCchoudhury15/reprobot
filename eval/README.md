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

## Limits

- Two projects, seven bugs, all through one command line program each
  (`xmllint` and `jq`).
- The false reports are written by hand. They are not real reports received
  by a project.
- Bugs that need a network peer use scripted loopback servers
  (`run.servers`). The curl case below is the first one, and it does not
  pass yet.

## curl (not passing yet)

`curl/` checks CVE-2023-38545, the SOCKS5 hostname overflow, with a scripted
SOCKS5 proxy on loopback. The build works on both commits, and a report about
a 200 byte hostname correctly gives `NOT_REPRODUCED`. But the CVE report
itself also gives `NOT_REPRODUCED` on the vulnerable commit, so `TestCurl`
fails and there is no `RESULTS.md` for curl.

The likely reason, from the curl advisory: the overflow needs a slow SOCKS5
handshake and a hostname longer than curl's download buffer (about 100 KB in
the curl tool unless `--limit-rate` makes it smaller). The current input
sends a 260 byte hostname in one step. It comes from curl's own regression
test for the fix (`tests/data/test728`), and that test only checks that the
fixed curl refuses the hostname with error 97. On the vulnerable commit the
same input makes curl go on without overflowing anything, so the test cannot
give a sanitizer crash. A working curl case needs either an input built for
the overflow conditions above or a different curl bug whose regression test
crashes under ASan.

