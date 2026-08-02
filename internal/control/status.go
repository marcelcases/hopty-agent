package control

import (
	"context"
	"time"
)

type AgentStatus struct {
	AgentVersion     string          `json:"agent_version"`
	Paired           bool            `json:"paired"`
	PasskeyCreatedAt *time.Time      `json:"passkey_created_at,omitempty"`
	LastAccessAt     *time.Time      `json:"last_access_at,omitempty"`
	Sessions         []SessionStatus `json:"sessions,omitempty"`
}

type SessionStatus struct {
	User       string `json:"user"`
	Connection string `json:"connection"`
	Transport  string `json:"transport"`
	LatencyMS  int    `json:"latency_ms"`
	IncomingIP string `json:"incoming_ip"`
}

func GetStatus(ctx context.Context, session *Session) (AgentStatus, error) {
	var status AgentStatus
	if err := session.request(ctx, "agent.status", map[string]any{}, "agent.status", &status); err != nil {
		return AgentStatus{}, err
	}
	return status, nil
}

func RevokeCredential(ctx context.Context, session *Session) error {
	var response map[string]any
	return session.request(ctx, "credential.revoke", map[string]any{}, "credential.revoked", &response)
}
