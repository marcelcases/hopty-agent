package control

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type PairingCreated struct {
	PairingURL       string    `json:"pairing_url"`
	VerificationCode string    `json:"verification_code"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func CreatePairing(ctx context.Context, connection *websocket.Conn) (PairingCreated, error) {
	if err := write(ctx, connection, "pairing.create", map[string]any{}); err != nil {
		return PairingCreated{}, err
	}
	var envelope Envelope
	if err := wsjson.Read(ctx, connection, &envelope); err != nil {
		return PairingCreated{}, err
	}
	if envelope.Version != Version || envelope.Type != "pairing.created" {
		return PairingCreated{}, errors.New("pairing request failed")
	}
	var created PairingCreated
	if err := json.Unmarshal(envelope.Payload, &created); err != nil || created.PairingURL == "" || len(created.VerificationCode) != 4 {
		return PairingCreated{}, errors.New("invalid pairing response")
	}
	return created, nil
}

func CancelPairing(ctx context.Context, connection *websocket.Conn) error {
	if err := write(ctx, connection, "pairing.cancel", map[string]any{}); err != nil {
		return err
	}
	var envelope Envelope
	if err := wsjson.Read(ctx, connection, &envelope); err != nil {
		return err
	}
	if envelope.Version != Version || envelope.Type != "pairing.cancelled" {
		return errors.New("pairing cancellation failed")
	}
	return nil
}
