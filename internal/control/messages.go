// Package control implements protocol-v1 agent control messages.
package control

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
)

const Version = 1

type Envelope struct {
	Version   int             `json:"version"`
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Payload   json.RawMessage `json:"payload"`
}

type Hello struct {
	AgentPublicKey string `json:"agent_public_key"`
	AgentVersion   string `json:"agent_version"`
	Protocol       []int  `json:"protocol_versions"`
}

type Challenge struct {
	Nonce string `json:"nonce"`
}
type Proof struct {
	Signature string `json:"signature"`
}
type Ready struct {
	AgentID string `json:"agent_id"`
	Paired  bool   `json:"paired"`
}

type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username"`
	Credential string   `json:"credential"`
}

type ActiveTerminals struct {
	TerminalIDs []string `json:"terminal_ids"`
}

type TerminalOpen struct {
	TerminalID string      `json:"terminal_id"`
	ICEServers []ICEServer `json:"ice_servers"`
}

type TerminalSignal struct {
	TerminalID string          `json:"terminal_id"`
	Kind       string          `json:"kind"`
	Data       json.RawMessage `json:"data"`
}

func NewRequestID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func Sign(privateKey ed25519.PrivateKey, nonce []byte) string {
	message := append([]byte("hopty-agent-auth-v1\n"), nonce...)
	return base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, message))
}

func Verify(publicKey ed25519.PublicKey, nonce []byte, encoded string) error {
	signature, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("invalid signature")
	}
	message := append([]byte("hopty-agent-auth-v1\n"), nonce...)
	if !ed25519.Verify(publicKey, message, signature) {
		return errors.New("invalid signature")
	}
	return nil
}
