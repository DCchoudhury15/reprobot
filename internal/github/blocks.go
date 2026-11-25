package github

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// File is one reporter file from a report.
type File struct {
	Name string
	Data []byte
}

// Submission is the reprobot part of a report.
type Submission struct {
	Repro []byte // repro.yaml
	Files []File
}

// ErrNoBlock means the report has no reprobot block. Most reports will not,
// and reprobot then stays silent.
var ErrNoBlock = errors.New("no reprobot block")

// Extract finds the reprobot blocks in a report text (Markdown):
//
//	```reprobot
//	version: 1
//	...
//	```
//
//	```reprobot-file input.bin
//	<base64>
//	```
//
//	```reprobot-file input.xml text
//	<the file as is>
//	```
//
// A file is base64 by default (spaces and line breaks are ignored), or the
// fenced text as is with "text". Fences are three or more backticks or
// tildes, and a fence closes with at least as many of the same character.
// Name checks are left to the caller, which checks every upload the same
// way.
func Extract(text string) (*Submission, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	sub := &Submission{}
	haveRepro := false
	for i := 0; i < len(lines); i++ {
		fence, info, ok := openFence(lines[i])
		if !ok {
			continue
		}
		// Find the closing fence; an unclosed fence runs to the end, as in
		// CommonMark.
		var body []string
		j := i + 1
		for ; j < len(lines); j++ {
			if closesFence(lines[j], fence) {
				break
			}
			body = append(body, lines[j])
		}
		fields := strings.Fields(info)
		kind := ""
		if len(fields) > 0 {
			kind = fields[0]
		}
		switch kind {
		case "reprobot":
			if haveRepro {
				return nil, errors.New("more than one ```reprobot block")
			}
			if j == len(lines) {
				return nil, errors.New("the ```reprobot block is not closed")
			}
			haveRepro = true
			sub.Repro = []byte(strings.Join(body, "\n") + "\n")
		case "reprobot-file":
			if j == len(lines) {
				return nil, errors.New("a ```reprobot-file block is not closed")
			}
			if len(fields) < 2 || len(fields) > 3 {
				return nil, fmt.Errorf("```reprobot-file needs a file name and optionally \"text\", got %q", truncate(info, 80))
			}
			f := File{Name: fields[1]}
			switch {
			case len(fields) == 3 && fields[2] == "text":
				f.Data = []byte(strings.Join(body, "\n") + "\n")
			case len(fields) == 3 && fields[2] != "base64":
				return nil, fmt.Errorf("file %q: unknown encoding %q, use base64 or text", truncate(f.Name, 80), truncate(fields[2], 20))
			default:
				data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(strings.Join(body, "")), ""))
				if err != nil {
					return nil, fmt.Errorf("file %q is not valid base64: %v", truncate(f.Name, 80), err)
				}
				f.Data = data
			}
			sub.Files = append(sub.Files, f)
		}
		i = j
	}
	if !haveRepro {
		if len(sub.Files) > 0 {
			return nil, errors.New("there are ```reprobot-file blocks but no ```reprobot block with the repro.yaml")
		}
		return nil, ErrNoBlock
	}
	return sub, nil
}

// openFence reads an opening code fence: up to three spaces, then three or
// more backticks or tildes, then the info string.
func openFence(line string) (fence, info string, ok bool) {
	s := strings.TrimLeft(line, " ")
	if len(line)-len(s) > 3 || len(s) < 3 || (s[0] != '`' && s[0] != '~') {
		return "", "", false
	}
	n := 0
	for n < len(s) && s[n] == s[0] {
		n++
	}
	if n < 3 {
		return "", "", false
	}
	info = strings.TrimSpace(s[n:])
	if s[0] == '`' && strings.Contains(info, "`") {
		return "", "", false
	}
	return s[:n], info, true
}

// closesFence says whether line closes a fence opened with fence.
func closesFence(line, fence string) bool {
	s := strings.TrimLeft(line, " ")
	if len(line)-len(s) > 3 {
		return false
	}
	s = strings.TrimRight(s, " \t")
	if len(s) < len(fence) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != fence[0] {
			return false
		}
	}
	return true
}
