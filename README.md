# reprobot

reprobot checks security reports by running them.

Open source projects now get more security reports than they can read, and
many are generated reports about bugs that do not exist. A report that reads
well and a real bug look the same on paper. The only reliable test is whether
the crash actually happens.

reprobot takes a small `repro.yaml` from the reporter, builds the exact commit
with sanitizers using a build profile the maintainers control, runs the proof
of concept in a sandbox with no network, and records what really happened:
reproduced as claimed, reproduced differently, not reproduced, flaky, and so on.
Reports that crash the same way are grouped together.

Status: early. See [docs/DESIGN.md](docs/DESIGN.md).
