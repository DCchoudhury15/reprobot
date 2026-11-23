package runner

import (
	"context"
	"fmt"

	"github.com/DCchoudhury15/reprobot/internal/result"
)

// CheckFix runs the report again on fixCommit, when before (the run on the
// report's own commit) reproduced, and returns the fix check. The fix
// commit is not built when before did not reproduce. An error is an
// infrastructure error of the fix run. run is Run, or a fake in tests.
func CheckFix(ctx context.Context, run func(context.Context, Options) (*result.Result, error), o Options, before *result.Result, fixCommit string) (*result.FixCheck, error) {
	var after *result.Result
	if before.Reproduced() {
		o.Commit = fixCommit
		var err error
		if after, err = run(ctx, o); err != nil {
			return nil, fmt.Errorf("fix commit: %w", err)
		}
		if after == nil {
			return nil, fmt.Errorf("fix commit: runner returned no result")
		}
	}
	state, reason := result.CheckFix(before, after)
	return &result.FixCheck{FixCommit: fixCommit, Fix: state, Reason: reason, Report: before, FixRun: after}, nil
}
