// Package localapi implements the agent's private Unix-socket control protocol.
package localapi

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const MaxMessageBytes = 4096

type Request struct {
	Command string `json:"command"`
}

type Status struct {
	AgentVersion     string          `json:"agent_version"`
	Connected        bool            `json:"connected"`
	Paired           bool            `json:"paired"`
	PairingVerified  bool            `json:"pairing_verified"`
	ActiveTerminals  int             `json:"active_terminals"`
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

type Pairing struct {
	URL       string `json:"url"`
	Code      string `json:"code"`
	ExpiresAt string `json:"expires_at"`
}

type Response struct {
	Status  *Status  `json:"status,omitempty"`
	Pairing *Pairing `json:"pairing,omitempty"`
	Error   string   `json:"error,omitempty"`
}

func ReadRequest(reader io.Reader) (Request, error) {
	var request Request
	if err := readJSON(reader, &request); err != nil {
		return Request{}, err
	}
	if request.Command == "" {
		return Request{}, errors.New("missing command")
	}
	return request, nil
}

func WriteResponse(writer io.Writer, response Response) error {
	return writeJSON(writer, response)
}

func Call(socket string, request Request) (Response, error) {
	connection, err := net.Dial("unix", socket)
	if err != nil {
		return Response{}, err
	}
	defer connection.Close()
	if err := writeJSON(connection, request); err != nil {
		return Response{}, err
	}
	var response Response
	if err := readJSON(connection, &response); err != nil {
		return Response{}, err
	}
	return response, nil
}

func readJSON(reader io.Reader, target any) error {
	var lengthBytes [4]byte
	if _, err := io.ReadFull(reader, lengthBytes[:]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(lengthBytes[:])
	if length == 0 || length > MaxMessageBytes {
		return errors.New("invalid message length")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(reader, data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func writeJSON(writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > MaxMessageBytes {
		return fmt.Errorf("invalid message length")
	}
	var lengthBytes [4]byte
	binary.BigEndian.PutUint32(lengthBytes[:], uint32(len(data)))
	if _, err := writer.Write(lengthBytes[:]); err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}
