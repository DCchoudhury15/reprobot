package github

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestExtract(t *testing.T) {
	text := "Intro text.\r\n\r\n" +
		"```reprobot\r\nversion: 1\r\nproject: tinyparse\r\n```\r\n\r\n" +
		"```reprobot-file input.bin\r\nyEFB\r\nQUE=\r\n```\r\n" +
		"~~~~ reprobot-file note.txt text\nline one\n```\nstill inside\n~~~~\n" +
		"```c\nint main(void) { return 0; }\n```\n" +
		"```reprobot-file b64.bin base64\nAAE=\n```\n"
	sub, err := Extract(text)
	if err != nil {
		t.Fatal(err)
	}
	if string(sub.Repro) != "version: 1\nproject: tinyparse\n" {
		t.Errorf("repro = %q", sub.Repro)
	}
	want := []File{
		{"input.bin", []byte{0xc8, 'A', 'A', 'A', 'A'}},
		{"note.txt", []byte("line one\n```\nstill inside\n")},
		{"b64.bin", []byte{0, 1}},
	}
	if !reflect.DeepEqual(sub.Files, want) {
		t.Errorf("files = %q\nwant  %q", sub.Files, want)
	}
}

func TestExtractNoBlock(t *testing.T) {
	for _, text := range []string{
		"",
		"A plain report with no code.",
		"```yaml\nversion: 1\n```",
		"Inline `reprobot` is not a block.",
		"``reprobot\nversion: 1\n``",           // two backticks are not a fence
		"    ```reprobot\nversion: 1\n    ```", // four spaces make a code block
	} {
		if _, err := Extract(text); !errors.Is(err, ErrNoBlock) {
			t.Errorf("%q: err = %v, want ErrNoBlock", text, err)
		}
	}
}

func TestExtractErrors(t *testing.T) {
	for text, want := range map[string]string{
		"```reprobot\na: 1\n```\n```reprobot\nb: 2\n```":          "more than one",
		"```reprobot\na: 1\n":                                     "not closed",
		"```reprobot\na: 1\n```\n```reprobot-file x\nAA==":        "not closed",
		"```reprobot\na: 1\n```\n```reprobot-file\nAA==\n```":     "needs a file name",
		"```reprobot\na: 1\n```\n```reprobot-file x hex\n00\n```": "unknown encoding",
		"```reprobot\na: 1\n```\n```reprobot-file x\n!!!\n```":    "not valid base64",
		"```reprobot-file x\nAA==\n```":                           "no ```reprobot block",
	} {
		_, err := Extract(text)
		if err == nil || errors.Is(err, ErrNoBlock) || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", text, err, want)
		}
	}
}

// A longer fence holds a shorter one, and only the same character closes.
func TestExtractFenceLengths(t *testing.T) {
	sub, err := Extract("`````reprobot\na: |\n  ```\n  ~~~~~\n`````\n")
	if err != nil {
		t.Fatal(err)
	}
	if string(sub.Repro) != "a: |\n  ```\n  ~~~~~\n" {
		t.Errorf("repro = %q", sub.Repro)
	}
}
