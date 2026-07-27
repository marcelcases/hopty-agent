package control

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Session owns the single reader for an agent control WebSocket.
type Session struct {
	connection *websocket.Conn
	writeMu    sync.Mutex
	responses  chan Envelope
	events     chan Envelope
}

func NewSession(connection *websocket.Conn) *Session {
	return &Session{connection: connection, responses: make(chan Envelope, 4), events: make(chan Envelope, 16)}
}

func (s *Session) Events() <-chan Envelope { return s.events }

func (s *Session) Run(ctx context.Context) error {
	defer close(s.events)
	for {
		var envelope Envelope
		if err := wsjson.Read(ctx, s.connection, &envelope); err != nil {
			return err
		}
		if envelope.Version != Version {
			return errors.New("unsupported control version")
		}
		if envelope.Type == "terminal.open" || envelope.Type == "terminal.signal" || envelope.Type == "terminal.close" {
			select {
			case s.events <- envelope:
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		select {
		case s.responses <- envelope:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Session) CloseNow() { s.connection.CloseNow() }

func (s *Session) request(ctx context.Context, typ string, payload any, responseType string, output any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	requestID, err := NewRequestID()
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	err = wsjson.Write(ctx, s.connection, Envelope{Version: Version, Type: typ, RequestID: requestID, Payload: data})
	s.writeMu.Unlock()
	if err != nil {
		return err
	}
	select {
	case envelope := <-s.responses:
		if envelope.Type != responseType {
			return errors.New("unexpected control response")
		}
		return json.Unmarshal(envelope.Payload, output)
	case <-ctx.Done():
		return ctx.Err()
	}
}
