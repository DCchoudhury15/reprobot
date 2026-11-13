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

## libxml2

Results: [libxml2/RESULTS.md](libxml2/RESULTS.md). Five CVEs, all reproduced
on the vulnerable commit and all gone on the fix commit. Three false reports,
none reproduced. A cold run (clone plus nine builds) takes about six minutes
on a laptop; one build takes about 35 seconds and a cached check 2 seconds.

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

## What this found

- CVE-2025-49795 and CVE-2025-49796 are two different bugs, but both are a
  SEGV with the same top three frames, so they get the same signature. They
  crash on different lines of the same function. The signature does not use
  line numbers because they change between commits. Grouping would merge
  these two reports.
- The titles of CVE-2025-49794 and CVE-2025-49796 name
  `xmlSchematronReportOutput`. In the real stack the crash is in
  `xmlSchematronFormatReport`, and the named function is not in the top
  frames. A report that copies the function from the CVE title gets
  `REPRODUCED_DIFFERENT`, not a rejection.
- With `--noout`, `xmllint` runs schematron checks in quiet mode and the bugs
  in the report text code are not reached. The command line in a report
  matters as much as the input file.

## Limits

- One project, five bugs, all found through the `xmllint` program.
- The false reports are written by hand. They are not real reports received
  by a project.
- Bugs that need a network peer (for example most curl bugs) cannot be tested
  yet.
