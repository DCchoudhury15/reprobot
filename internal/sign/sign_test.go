package sign

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// The example from the DSSE protocol spec.
func TestPAE(t *testing.T) {
	got := string(pae("http://example.com/HelloWorld", []byte("hello world")))
	if want := "DSSEv1 29 http://example.com/HelloWorld 11 hello world"; got != want {
		t.Errorf("pae = %q, want %q", got, want)
	}
}

func TestSignVerify(t *testing.T) {
	pub, priv := newKey(t)
	otherPub, otherPriv := newKey(t)
	payload := []byte(`{"state":"REPRODUCED_AS_CLAIMED","inputs":{"commit":"abc"}}`)
	env := Sign(priv, payload)
	if env.Signatures[0].KeyID != KeyID(pub) || len(KeyID(pub)) != 64 {
		t.Errorf("keyid %q, want %q", env.Signatures[0].KeyID, KeyID(pub))
	}
	data, _ := json.Marshal(env)
	dec, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(dec, pub)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("Verify = %q, %v", got, err)
	}
	if _, err := Verify(dec, otherPub); !errors.Is(err, ErrNoValidSignature) {
		t.Errorf("other key: %v", err)
	}

	// One good signature among bad ones is enough; the key id is not
	// trusted, so a wrong one does not matter.
	multi := Sign(otherPriv, payload)
	good := env.Signatures[0]
	good.KeyID = "wrong"
	multi.Signatures = append(multi.Signatures, Signature{KeyID: KeyID(pub), Sig: "!!"}, good)
	if _, err := Verify(multi, pub); err != nil {
		t.Errorf("good signature among others: %v", err)
	}

	// A good signature by the same key over another payload type is not
	// a result.
	otherType := &Envelope{PayloadType: "application/json", Payload: env.Payload, Signatures: []Signature{{
		KeyID: KeyID(pub),
		Sig:   base64.StdEncoding.EncodeToString(ed25519.Sign(priv, pae("application/json", payload))),
	}}}
	if _, err := Verify(otherType, pub); err == nil {
		t.Error("verified a signature over another payload type")
	}

	tamper := map[string]func(e *Envelope){
		"payload": func(e *Envelope) {
			e.Payload = base64.StdEncoding.EncodeToString(bytes.Replace(payload, []byte("abc"), []byte("abd"), 1))
		},
		"payload type": func(e *Envelope) { e.PayloadType = "application/json" },
		"signature": func(e *Envelope) {
			sig, _ := base64.StdEncoding.DecodeString(e.Signatures[0].Sig)
			sig[0] ^= 1
			e.Signatures[0].Sig = base64.StdEncoding.EncodeToString(sig)
		},
		"no signatures":  func(e *Envelope) { e.Signatures = nil },
		"payload base64": func(e *Envelope) { e.Payload = "%%%" },
	}
	for name, f := range tamper {
		e := *env
		e.Signatures = append([]Signature(nil), env.Signatures...)
		f(&e)
		if _, err := Verify(&e, pub); err == nil {
			t.Errorf("tampered %s: verified", name)
		}
	}
}

func TestFixPayloadType(t *testing.T) {
	pub, priv := newKey(t)
	payload := []byte(`{"fix":"FIXED"}`)
	fix := SignAs(priv, FixPayloadType, payload)

	// A signed fix check is not a signed result.
	if _, err := Verify(fix, pub); err == nil {
		t.Error("Verify took a fix check as a result")
	}
	got, err := VerifyAs(fix, pub, PayloadType, FixPayloadType)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("VerifyAs = %q, %v", got, err)
	}
	// The type is signed: relabeling the envelope breaks it.
	relabeled := *fix
	relabeled.PayloadType = PayloadType
	if _, err := VerifyAs(&relabeled, pub, PayloadType, FixPayloadType); !errors.Is(err, ErrNoValidSignature) {
		t.Errorf("relabeled fix envelope: %v, want ErrNoValidSignature", err)
	}
}

func TestDecodeRejects(t *testing.T) {
	_, priv := newKey(t)
	data, _ := json.Marshal(Sign(priv, []byte("{}")))
	for name, in := range map[string]string{
		"trailing data": string(data) + "{}",
		"unknown field": `{"payloadType":"x","payload":"","signatures":[],"extra":1}`,
		"not json":      "nope",
	} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("%s: nil error", name)
		}
	}
}

func TestKeyFiles(t *testing.T) {
	dir := t.TempDir()
	privPath, pubPath := filepath.Join(dir, "k"), filepath.Join(dir, "k.pub")
	pub, err := GenerateKey(privPath, pubPath)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := LoadPrivateKey(privPath)
	if err != nil {
		t.Fatal(err)
	}
	pub2, err := LoadPublicKey(pubPath)
	if err != nil || !pub2.Equal(pub) || !priv.Public().(ed25519.PublicKey).Equal(pub) {
		t.Fatalf("loaded keys do not match: %v", err)
	}
	if _, err := Verify(Sign(priv, []byte("x")), pub2); err != nil {
		t.Error(err)
	}

	if _, err := GenerateKey(privPath, filepath.Join(dir, "new.pub")); err == nil {
		t.Error("GenerateKey overwrote a private key")
	}
	if _, err := GenerateKey(filepath.Join(dir, "new"), pubPath); err == nil {
		t.Error("GenerateKey overwrote a public key")
	}
	if _, err := os.Stat(filepath.Join(dir, "new")); !os.IsNotExist(err) {
		t.Error("a failed GenerateKey left its private key behind")
	}

	os.Chmod(privPath, 0o640)
	if _, err := LoadPrivateKey(privPath); err == nil {
		t.Error("loaded a group-readable private key")
	}
	os.Chmod(privPath, 0o600)
	os.Chmod(pubPath, 0o600)
	if _, err := LoadPrivateKey(pubPath); err == nil {
		t.Error("loaded a public key as private")
	}
	if _, err := LoadPublicKey(privPath); err == nil {
		t.Error("loaded a private key as public")
	}
}
