package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/sign"
)

const keygenUsage = `usage: reprobot keygen DIR

Writes a new Ed25519 signing key to DIR/signing.key (mode 0600) and its
public key to DIR/signing.pub, and prints the key id. Give the private key
to "reprobot run --sign-key" and publish the public key, so others can run
"reprobot verify".`

const verifyUsage = `usage: reprobot verify --key signing.pub ENVELOPE

Checks a signed result or a signed fix check (a file, or - for stdin).
When the signature is good it prints the signed JSON to stdout and a
summary to stderr, and exits 0. Otherwise it exits 1.`

func writeIndented(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func keygenCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] == "" || args[0][0] == '-' {
		fmt.Fprintln(stderr, keygenUsage)
		return 2
	}
	dir := args[0]
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintln(stderr, "reprobot keygen:", err)
		return 1
	}
	pub, err := sign.GenerateKey(filepath.Join(dir, "signing.key"), filepath.Join(dir, "signing.pub"))
	if err != nil {
		fmt.Fprintln(stderr, "reprobot keygen:", err)
		return 1
	}
	fmt.Fprintln(stdout, sign.KeyID(pub))
	return 0
}

func verifyCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var keyPath, envPath string
	switch {
	case len(args) == 3 && args[0] == "--key":
		keyPath, envPath = args[1], args[2]
	case len(args) == 3 && args[1] == "--key":
		envPath, keyPath = args[0], args[2]
	default:
		fmt.Fprintln(stderr, verifyUsage)
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "reprobot verify:", err)
		return 1
	}
	pub, err := sign.LoadPublicKey(keyPath)
	if err != nil {
		return fail(err)
	}
	var data []byte
	if envPath == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, 64<<20))
	} else {
		data, err = os.ReadFile(envPath)
	}
	if err != nil {
		return fail(err)
	}
	env, err := sign.Decode(data)
	if err != nil {
		return fail(err)
	}
	payload, err := sign.VerifyAs(env, pub, sign.PayloadType, sign.FixPayloadType)
	if err != nil {
		return fail(err)
	}
	var summary string
	if env.PayloadType == sign.FixPayloadType {
		var fc result.FixCheck
		if err := json.Unmarshal(payload, &fc); err != nil || fc.Report == nil {
			return fail(fmt.Errorf("signed payload is not a fix check: %v", err))
		}
		in := fc.Report.Inputs
		summary = fmt.Sprintf("fix check %s: %s for %s, report commit %s, fix commit %s (profile %s, reproducer %s)",
			fc.Fix, fc.Reason, in.Project, in.Commit, fc.FixCommit, in.Profile, in.ReproducerHash)
	} else {
		var res result.Result
		if err := json.Unmarshal(payload, &res); err != nil {
			return fail(fmt.Errorf("signed payload is not a result: %w", err))
		}
		in := res.Inputs
		summary = fmt.Sprintf("%s for %s at %s (profile %s, reproducer %s)",
			res.State, in.Project, in.Commit, in.Profile, in.ReproducerHash)
	}
	if _, err := stdout.Write(payload); err != nil {
		return fail(err)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stderr, "good signature by key %s\n%s\n", sign.KeyID(pub), summary)
	return 0
}
