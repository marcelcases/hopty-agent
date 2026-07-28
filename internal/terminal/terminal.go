// Package terminal owns direct WebRTC DataChannel terminal sessions.
package terminal

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/marcelcases/hopty-agent/internal/control"
	"github.com/pion/webrtc/v4"
)

const (
	label    = "hopty.terminal.v1"
	maxFrame = 64 << 10
)

type Manager struct {
	mu        sync.Mutex
	terminals map[string]*session
	send      func(context.Context, string, any) error
}
type session struct {
	mu        sync.Mutex
	id        string
	config    webrtc.Configuration
	manager   *Manager
	pc        *webrtc.PeerConnection
	dc        *webrtc.DataChannel
	pty       *os.File
	cmd       *exec.Cmd
	closed    bool
	closeOnce sync.Once
	recovery  *time.Timer
}

func New(send func(context.Context, string, any) error) *Manager {
	return &Manager{terminals: make(map[string]*session), send: send}
}

func (m *Manager) Open(open control.TerminalOpen) error {
	if open.TerminalID == "" {
		return errors.New("invalid terminal")
	}
	ice := make([]webrtc.ICEServer, 0, len(open.ICEServers))
	for _, server := range open.ICEServers {
		ice = append(ice, webrtc.ICEServer{URLs: server.URLs, Username: server.Username, Credential: server.Credential})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.terminals[open.TerminalID]; exists {
		return errors.New("terminal already exists")
	}
	m.terminals[open.TerminalID] = &session{id: open.TerminalID, config: webrtc.Configuration{ICEServers: ice}, manager: m}
	return nil
}

func (m *Manager) Signal(ctx context.Context, signal control.TerminalSignal) error {
	m.mu.Lock()
	terminal := m.terminals[signal.TerminalID]
	m.mu.Unlock()
	if terminal == nil {
		return errors.New("unknown terminal")
	}
	switch signal.Kind {
	case "offer", "ice_restart":
		return terminal.offer(ctx, signal.Data)
	case "close":
		terminal.close("browser_closed")
		return nil
	default:
		return errors.New("unsupported signal")
	}
}

func (t *session) offer(ctx context.Context, raw json.RawMessage) error {
	var description webrtc.SessionDescription
	if err := json.Unmarshal(raw, &description); err != nil || description.Type != webrtc.SDPTypeOffer || description.SDP == "" {
		return errors.New("invalid offer")
	}
	t.mu.Lock()
	pc := t.pc
	t.mu.Unlock()
	if pc == nil {
		var err error
		pc, err = webrtc.NewPeerConnection(t.config)
		if err != nil {
			return err
		}
		pc.OnDataChannel(func(dc *webrtc.DataChannel) {
			if dc.Label() != label || t.setDataChannel(dc) != nil {
				_ = dc.Close()
				return
			}
			dc.OnOpen(func() {
				if t.startPTY() != nil {
					t.close("pty_start_failed")
					return
				}
				_ = t.manager.send(context.Background(), "terminal.signal", control.TerminalSignal{TerminalID: t.id, Kind: "active", Data: json.RawMessage(`{}`)})
			})
			dc.OnMessage(t.message)
		})
		pc.OnConnectionStateChange(t.connectionState)
		t.mu.Lock()
		t.pc = pc
		t.mu.Unlock()
	}
	if err := pc.SetRemoteDescription(description); err != nil {
		return err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return err
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		return errors.New("ice gathering timeout")
	}
	encoded, err := json.Marshal(pc.LocalDescription())
	if err != nil {
		return err
	}
	return t.manager.send(ctx, "terminal.signal", control.TerminalSignal{TerminalID: t.id, Kind: "answer", Data: encoded})
}

func (t *session) setDataChannel(dc *webrtc.DataChannel) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dc != nil || t.closed {
		return errors.New("unavailable")
	}
	t.dc = dc
	return nil
}
func (t *session) startPTY() error {
	t.mu.Lock()
	if t.closed || t.pty != nil {
		t.mu.Unlock()
		return errors.New("unavailable")
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Env = terminalEnvironment(os.Environ())
	ptmx, err := pty.Start(cmd)
	if err == nil {
		t.cmd, t.pty = cmd, ptmx
	}
	t.mu.Unlock()
	if err != nil {
		return err
	}
	go t.copyOutput(ptmx)
	go func() { _ = cmd.Wait(); t.close("shell_exit") }()
	return nil
}
func terminalEnvironment(environment []string) []string {
	filtered := make([]string, 0, len(environment)+3)
	for _, variable := range environment {
		if strings.HasPrefix(variable, "TERM=") || strings.HasPrefix(variable, "COLORTERM=") || strings.HasPrefix(variable, "TERM_PROGRAM=") {
			continue
		}
		filtered = append(filtered, variable)
	}
	return append(filtered, "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=hopty")
}

func (t *session) copyOutput(ptmx *os.File) {
	buffer := make([]byte, maxFrame-1)
	for {
		n, err := ptmx.Read(buffer)
		if n > 0 {
			t.mu.Lock()
			dc, closed := t.dc, t.closed
			t.mu.Unlock()
			if !closed && dc != nil {
				_ = dc.Send(append([]byte{1}, buffer[:n]...))
			}
		}
		if err != nil {
			return
		}
	}
}
func (t *session) message(message webrtc.DataChannelMessage) {
	if message.IsString || len(message.Data) == 0 || len(message.Data) > maxFrame {
		t.close("invalid_frame")
		return
	}
	t.mu.Lock()
	ptmx, closed := t.pty, t.closed
	t.mu.Unlock()
	if closed || ptmx == nil {
		t.close("terminal_unavailable")
		return
	}
	switch message.Data[0] {
	case 1:
		if _, err := ptmx.Write(message.Data[1:]); err != nil {
			t.close("pty_write_failed")
		}
	case 2:
		if len(message.Data) != 5 {
			t.close("invalid_resize")
			return
		}
		cols, rows := binary.BigEndian.Uint16(message.Data[1:3]), binary.BigEndian.Uint16(message.Data[3:5])
		if cols == 0 || rows == 0 || cols > 1000 || rows > 1000 || pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows}) != nil {
			t.close("invalid_resize")
		}
	default:
		t.close("invalid_frame")
	}
}
func (t *session) connectionState(state webrtc.PeerConnectionState) {
	switch state {
	case webrtc.PeerConnectionStateDisconnected:
		t.mu.Lock()
		if t.recovery == nil && !t.closed {
			t.recovery = time.AfterFunc(10*time.Second, func() { t.close("recovery_timeout") })
		}
		t.mu.Unlock()
	case webrtc.PeerConnectionStateConnected:
		t.mu.Lock()
		if t.recovery != nil {
			t.recovery.Stop()
			t.recovery = nil
		}
		t.mu.Unlock()
	case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
		t.close("channel_closed")
	}
}
func (t *session) close(reason string) {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		if t.recovery != nil {
			t.recovery.Stop()
		}
		ptmx, dc, pc := t.pty, t.dc, t.pc
		t.mu.Unlock()
		if ptmx != nil {
			_ = ptmx.Close()
		}
		if dc != nil {
			_ = dc.Close()
		}
		if pc != nil {
			_ = pc.Close()
		}
		t.manager.mu.Lock()
		delete(t.manager.terminals, t.id)
		t.manager.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = t.manager.send(ctx, "terminal.signal", control.TerminalSignal{TerminalID: t.id, Kind: "close", Data: json.RawMessage(`{"reason":"` + reason + `"}`)})
	})
}
func (m *Manager) ActiveIDs() []string {
	m.mu.Lock()
	ids := make([]string, 0, len(m.terminals))
	for id := range m.terminals {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	return ids
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	all := make([]*session, 0, len(m.terminals))
	for _, terminal := range m.terminals {
		all = append(all, terminal)
	}
	m.mu.Unlock()
	for _, terminal := range all {
		terminal.close("agent_shutdown")
	}
}
