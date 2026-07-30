package control

import (
	"context"
	"errors"
	"time"
)

type PairingCreated struct {
	PairingURL       string    `json:"pairing_url"`
	VerificationCode string    `json:"verification_code"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func CreatePairing(ctx context.Context, session *Session, request PairingCreate) (PairingCreated, error) {
	var created PairingCreated
	if err := session.request(ctx, "pairing.create", request, "pairing.created", &created); err != nil {
		return PairingCreated{}, err
	}
	if created.PairingURL == "" || len(created.VerificationCode) != 4 {
		return PairingCreated{}, errors.New("invalid pairing response")
	}
	return created, nil
}

func CancelPairing(ctx context.Context, session *Session) error {
	var response map[string]any
	return session.request(ctx, "pairing.cancel", map[string]any{}, "pairing.cancelled", &response)
}
