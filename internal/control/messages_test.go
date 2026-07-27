package control

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	nonce := []byte("01234567890123456789012345678901")
	if err := Verify(publicKey, nonce, Sign(privateKey, nonce)); err != nil {
		t.Fatal(err)
	}
	if err := Verify(publicKey, nonce, base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))); err == nil {
		t.Fatal("invalid signature accepted")
	}
}
