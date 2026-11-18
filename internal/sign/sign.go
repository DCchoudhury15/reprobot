// Package sign signs results so anyone with the public key can check that
// a verdict came from this reprobot and was not edited.
//
// A signed result is a DSSE envelope (github.com/secure-systems-lab/dsse):
// the payload is the result JSON, signed with Ed25519 over the DSSE
// pre-authentication encoding. The signature covers the payload bytes
// exactly, so no JSON canonicalisation is needed.
package sign

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strconv"
)

// PayloadType is the DSSE payload type of a signed result.
const PayloadType = "application/vnd.reprobot.result+json"

// Envelope is a DSSE envelope.
type Envelope struct {
	PayloadType string      `json:"payloadType"`
	Payload     string      `json:"payload"` // standard base64
	Signatures  []Signature `json:"signatures"`
}

// Signature is one signature in an Envelope.
type Signature struct {
	KeyID string `json:"keyid"`
	Sig   string `json:"sig"` // standard base64
}

// KeyID names a public key: the hex sha256 of its DER encoding.
func KeyID(pub ed25519.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		// Only fails for key types x509 does not know.
		panic(err)
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// pae is the DSSE pre-authentication encoding.
func pae(payloadType string, payload []byte) []byte {
	var b bytes.Buffer
	b.WriteString("DSSEv1 ")
	b.WriteString(strconv.Itoa(len(payloadType)))
	b.WriteByte(' ')
	b.WriteString(payloadType)
	b.WriteByte(' ')
	b.WriteString(strconv.Itoa(len(payload)))
	b.WriteByte(' ')
	b.Write(payload)
	return b.Bytes()
}

// Sign wraps payload in an envelope signed with key.
func Sign(key ed25519.PrivateKey, payload []byte) *Envelope {
	pub := key.Public().(ed25519.PublicKey)
	return &Envelope{
		PayloadType: PayloadType,
		Payload:     base64.StdEncoding.EncodeToString(payload),
		Signatures: []Signature{{
			KeyID: KeyID(pub),
			Sig:   base64.StdEncoding.EncodeToString(ed25519.Sign(key, pae(PayloadType, payload))),
		}},
	}
}

// ErrNoValidSignature means no signature in the envelope verifies with the
// given key.
var ErrNoValidSignature = errors.New("sign: no valid signature for this key")

// Verify returns the payload when one signature of env verifies with pub.
// The key id is only a hint: every signature is checked against pub.
func Verify(env *Envelope, pub ed25519.PublicKey) ([]byte, error) {
	if env.PayloadType != PayloadType {
		return nil, fmt.Errorf("sign: payload type %q, want %q", env.PayloadType, PayloadType)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("sign: payload is not base64: %w", err)
	}
	msg := pae(env.PayloadType, payload)
	for _, s := range env.Signatures {
		sig, err := base64.StdEncoding.DecodeString(s.Sig)
		if err != nil || len(sig) != ed25519.SignatureSize {
			continue
		}
		if ed25519.Verify(pub, msg, sig) {
			return payload, nil
		}
	}
	return nil, ErrNoValidSignature
}

// GenerateKey writes a new private key (PKCS #8 PEM, mode 0600) to
// privPath and its public key (PKIX PEM) to pubPath. It refuses to
// overwrite either file.
func GenerateKey(privPath, pubPath string) (ed25519.PublicKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	if err := writeNew(privPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600); err != nil {
		return nil, err
	}
	if err := writeNew(pubPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o644); err != nil {
		os.Remove(privPath)
		return nil, err
	}
	return pub, nil
}

func writeNew(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

func readPEM(path, wantType string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != wantType || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("%s: want exactly one %s PEM block", path, wantType)
	}
	return block.Bytes, nil
}

// LoadPrivateKey reads a private key written by GenerateKey. It refuses a
// file that other users can read.
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s: private key is readable by other users (mode %v); chmod 600 it", path, fi.Mode().Perm())
	}
	der, err := readPEM(path, "PRIVATE KEY")
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an Ed25519 key", path)
	}
	return priv, nil
}

// LoadPublicKey reads a public key written by GenerateKey.
func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	der, err := readPEM(path, "PUBLIC KEY")
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an Ed25519 key", path)
	}
	return pub, nil
}

// Decode parses an envelope, rejecting unknown fields and trailing data.
func Decode(data []byte) (*Envelope, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var env Envelope
	if err := dec.Decode(&env); err != nil {
		return nil, fmt.Errorf("sign: bad envelope: %w", err)
	}
	if dec.More() {
		return nil, errors.New("sign: data after the envelope")
	}
	return &env, nil
}
