package control

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"path"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func Connect(ctx context.Context, serviceURL *url.URL, privateKey ed25519.PrivateKey, agentVersion string) (Ready, *websocket.Conn, error) {
	endpoint := *serviceURL
	switch endpoint.Scheme {
	case "https":
		endpoint.Scheme = "wss"
	case "http":
		endpoint.Scheme = "ws"
	default:
		return Ready{}, nil, errors.New("unsupported service URL scheme")
	}
	endpoint.Path = path.Join(endpoint.Path, "/api/v1/agent/control")
	connection, _, err := websocket.Dial(ctx, endpoint.String(), nil)
	if err != nil {
		return Ready{}, nil, err
	}
	fail := func(err error) (Ready, *websocket.Conn, error) {
		connection.CloseNow()
		return Ready{}, nil, err
	}
	if err := write(ctx, connection, "agent.hello", Hello{
		AgentPublicKey: base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		AgentVersion:   agentVersion,
		Protocol:       []int{Version},
	}); err != nil {
		return fail(err)
	}
	var challengeEnvelope Envelope
	if err := wsjson.Read(ctx, connection, &challengeEnvelope); err != nil {
		return fail(err)
	}
	if challengeEnvelope.Version != Version || challengeEnvelope.Type != "agent.challenge" {
		return fail(errors.New("invalid challenge"))
	}
	var challenge Challenge
	if err := json.Unmarshal(challengeEnvelope.Payload, &challenge); err != nil {
		return fail(err)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(challenge.Nonce)
	if err != nil || len(nonce) != 32 {
		return fail(errors.New("invalid challenge"))
	}
	if err := write(ctx, connection, "agent.prove", Proof{Signature: Sign(privateKey, nonce)}); err != nil {
		return fail(err)
	}
	var readyEnvelope Envelope
	if err := wsjson.Read(ctx, connection, &readyEnvelope); err != nil {
		return fail(err)
	}
	if readyEnvelope.Version != Version || readyEnvelope.Type != "agent.ready" {
		return fail(errors.New("invalid ready"))
	}
	var ready Ready
	if err := json.Unmarshal(readyEnvelope.Payload, &ready); err != nil || ready.AgentID == "" {
		return fail(errors.New("invalid ready"))
	}
	return ready, connection, nil
}

func write(ctx context.Context, connection *websocket.Conn, typ string, payload any) error {
	requestID, err := NewRequestID()
	if err != nil {
		return err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return wsjson.Write(ctx, connection, Envelope{Version: Version, Type: typ, RequestID: requestID, Payload: data})
}
