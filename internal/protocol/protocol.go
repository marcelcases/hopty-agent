// Package protocol implements Hopty protocol version 1 codecs.
package protocol

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const (
	Version         = 1
	MaxMessageBytes = 64 << 10
	MaxFrameBytes   = 64 << 10
)

var (
	ErrInvalidMessage = errors.New("invalid protocol message")
	ErrUnsupported    = errors.New("unsupported protocol version")
)

type Envelope struct {
	Version   int             `json:"version"`
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Payload   json.RawMessage `json:"payload"`
}

func DecodeEnvelope(data []byte, allowedTypes map[string]struct{}) (Envelope, error) {
	if len(data) == 0 || len(data) > MaxMessageBytes || hasDuplicateKeys(data) {
		return Envelope{}, ErrInvalidMessage
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope Envelope
	if err := decoder.Decode(&envelope); err != nil {
		return Envelope{}, ErrInvalidMessage
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Envelope{}, ErrInvalidMessage
	}
	if envelope.Version != Version {
		return Envelope{}, ErrUnsupported
	}
	if _, ok := allowedTypes[envelope.Type]; !ok || len(envelope.Payload) == 0 || envelope.Payload[0] != '{' {
		return Envelope{}, ErrInvalidMessage
	}
	requestID, err := base64.RawURLEncoding.DecodeString(envelope.RequestID)
	if err != nil || len(requestID) != 16 {
		return Envelope{}, ErrInvalidMessage
	}
	return envelope, nil
}

// DecodePayload rejects unknown and duplicate fields in the typed payload.
func DecodePayload(payload json.RawMessage, target any) error {
	if len(payload) == 0 || hasDuplicateKeys(payload) {
		return ErrInvalidMessage
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidMessage
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidMessage
	}
	return nil
}

const (
	FramePTY    byte = 0x01
	FrameResize byte = 0x02
	FrameExit   byte = 0x03
	FrameError  byte = 0x04
)

type Frame struct {
	Type byte
	Data []byte
}

func DecodeFrame(data []byte) (Frame, error) {
	if len(data) == 0 || len(data) > MaxFrameBytes {
		return Frame{}, ErrInvalidMessage
	}
	frame := Frame{Type: data[0], Data: append([]byte(nil), data[1:]...)}
	switch frame.Type {
	case FramePTY:
		return frame, nil
	case FrameResize:
		if len(frame.Data) != 4 {
			return Frame{}, ErrInvalidMessage
		}
		columns, rows := binary.BigEndian.Uint16(frame.Data[:2]), binary.BigEndian.Uint16(frame.Data[2:])
		if columns == 0 || rows == 0 || columns > 1000 || rows > 1000 {
			return Frame{}, ErrInvalidMessage
		}
	case FrameExit:
		if len(frame.Data) != 5 {
			return Frame{}, ErrInvalidMessage
		}
	case FrameError:
		if len(frame.Data) > 256 || !utf8.Valid(frame.Data) {
			return Frame{}, ErrInvalidMessage
		}
	default:
		return Frame{}, ErrInvalidMessage
	}
	return frame, nil
}

func EncodeResize(columns, rows uint16) ([]byte, error) {
	if columns == 0 || rows == 0 || columns > 1000 || rows > 1000 {
		return nil, ErrInvalidMessage
	}
	frame := []byte{FrameResize, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(frame[1:3], columns)
	binary.BigEndian.PutUint16(frame[3:5], rows)
	return frame, nil
}

func hasDuplicateKeys(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value func() bool
	value = func() bool {
		token, err := decoder.Token()
		if err != nil {
			return true
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return false
		}
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok {
					return true
				}
				if _, exists := seen[name]; exists {
					return true
				}
				seen[name] = struct{}{}
				if value() {
					return true
				}
			}
			_, err := decoder.Token()
			return err != nil
		case '[':
			for decoder.More() {
				if value() {
					return true
				}
			}
			_, err := decoder.Token()
			return err != nil
		default:
			return true
		}
	}
	if value() {
		return true
	}
	_, err := decoder.Token()
	return !errors.Is(err, io.EOF)
}
