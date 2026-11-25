package github

import (
	"fmt"
	"strings"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/sanitizer"
)

// CommentBody is the advisory comment for a finished check. link, when set,
// is the result page. Text that comes from the report or the program (crash
// functions, claim findings, reasons) goes into code spans, so it cannot
// mention people or add Markdown.
func CommentBody(res *result.Result, fix *result.FixCheck, link string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**reprobot: %s**\n\n", res.State)
	fmt.Fprintf(&b, "%s\n\n", code(res.Reason))
	in := res.Inputs
	if in.Commit != "" {
		fmt.Fprintf(&b, "- Tested: %s at %s, profile %s\n", code(in.Project), code(short(in.Commit)), code(in.Profile))
	}
	if c := res.Crash; c != nil {
		// The first frame of the project, not of the sanitizer runtime
		// or libc (memcpy and the like).
		top := ""
		if pf := sanitizer.ProjectFrames(c); len(pf) > 0 {
			top = pf[0].Function
		}
		fmt.Fprintf(&b, "- Crash: %s %s", code(c.Sanitizer), code(c.Kind))
		if top != "" {
			fmt.Fprintf(&b, " in %s", code(top))
		}
		fmt.Fprintf(&b, ", signature %s\n", code(short(c.Signature)))
	}
	if len(res.Runs) > 0 {
		fmt.Fprintf(&b, "- Runs: %d of %d crashed\n", res.Crashed, len(res.Runs))
	}
	if fix != nil {
		fmt.Fprintf(&b, "- Fix check on %s: **%s**, %s\n", code(fix.FixCommit), fix.Fix, code(fix.Reason))
	}
	if len(res.ClaimFindings) > 0 {
		b.WriteString("\nThe report text says things the code does not show:\n\n")
		for i, f := range res.ClaimFindings {
			if i == 10 {
				fmt.Fprintf(&b, "- and %d more\n", len(res.ClaimFindings)-10)
				break
			}
			fmt.Fprintf(&b, "- %s\n", code(f))
		}
	}
	if link != "" {
		fmt.Fprintf(&b, "\nFull result: %s\n", link)
	}
	b.WriteString("\nreprobot built the project at that commit and ran the reproducer in a sandbox. A crash counts only when the sanitizer itself reports it.\n")
	return b.String()
}

// RejectBody is the advisory comment for a reprobot block that could not be
// taken in.
func RejectBody(reason string) string {
	return "**reprobot could not take this report in**\n\n" + code(reason) +
		"\n\nFix the `reprobot` block in the report and submit it again. The format is in https://github.com/DCchoudhury15/reprobot/blob/main/docs/SERVICE.md#github-app\n"
}

// code puts text in a Markdown code span. Backticks are replaced, line
// breaks are joined and the length is capped.
func code(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "`", "'")
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	if s == "" {
		return "``"
	}
	return "`" + s + "`"
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
