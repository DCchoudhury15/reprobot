package server

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"

	"github.com/DCchoudhury15/reprobot/internal/result"
)

// topFrames is how many stack frames the page shows.
const topFrames = 5

// pageCSP allows no scripts at all; the page only needs its inline style.
const pageCSP = "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

type pageData struct {
	*reportView
	Crash  *result.Crash
	Frames []string
}

var pageTmpl = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>reprobot report {{.ReportID}}</title>
<style>
body { font-family: system-ui, sans-serif; max-width: 52rem; margin: 2rem auto; padding: 0 1rem; line-height: 1.5; }
th { text-align: left; padding-right: 1rem; vertical-align: top; }
pre, code { font-family: ui-monospace, monospace; }
pre { background: #f4f4f4; padding: .5rem; overflow-x: auto; white-space: pre-wrap; }
</style>
</head>
<body>
<h1>Report {{.ReportID}}</h1>
<table>
<tr><th>Project</th><td>{{.Project}}</td></tr>
{{if .Reporter}}<tr><th>Reporter</th><td>{{.Reporter}}</td></tr>{{end}}
<tr><th>Job state</th><td>{{.State}} (attempt {{.Attempts}} of {{.MaxAttempts}})</td></tr>
{{if .LastError}}<tr><th>Last error</th><td><pre>{{.LastError}}</pre></td></tr>{{end}}
</table>
{{with .Result}}
<h2>Result: {{.State}}</h2>
<p>{{.Reason}}</p>
<table>
<tr><th>Runs that crashed</th><td>{{.Crashed}} of {{len .Runs}}</td></tr>
<tr><th>Commit</th><td><code>{{.Inputs.Commit}}</code></td></tr>
<tr><th>Profile</th><td>{{.Inputs.Profile}}</td></tr>
</table>
{{else}}
<p>No result yet.</p>
{{end}}
{{with .Crash}}
<h2>Crash</h2>
<table>
<tr><th>Kind</th><td>{{.Kind}}</td></tr>
<tr><th>Sanitizer</th><td>{{.Sanitizer}}</td></tr>
<tr><th>Signature</th><td><code>{{.Signature}}</code></td></tr>
</table>
{{end}}
{{if .Frames}}
<h3>Top frames</h3>
<pre>{{range .Frames}}{{.}}
{{end}}</pre>
{{end}}
{{with .Result}}{{if .ClaimFindings}}
<h2>Claim findings</h2>
<ul>{{range .ClaimFindings}}<li>{{.}}</li>{{end}}</ul>
{{end}}{{end}}
<h2>Duplicates</h2>
{{if .Duplicates}}<ul>{{range .Duplicates}}<li><a href="/reports/{{.}}">{{.}}</a></li>{{end}}</ul>
{{else}}<p>None found.</p>{{end}}
</body>
</html>
`))

func (s *Server) reportPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", pageCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	v, err := s.loadReport(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if v == nil {
		http.Error(w, "report not found", http.StatusNotFound)
		return
	}
	d := pageData{reportView: v}
	if v.Result != nil && v.Result.Crash != nil {
		d.Crash = v.Result.Crash
		for i, f := range d.Crash.Frames {
			if i == topFrames {
				break
			}
			line := fmt.Sprintf("#%d %s", f.Index, f.Function)
			if f.File != "" {
				line += fmt.Sprintf(" %s:%d", f.File, f.Line)
			}
			d.Frames = append(d.Frames, line)
		}
	}
	var buf bytes.Buffer
	if err := pageTmpl.Execute(&buf, d); err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}
