package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/pion/webrtc/v4"
)

const (
	dataChannelLabel = "hopty.terminal.v1"
	maxSignalBytes   = 32 << 10
	maxFrameBytes    = 64 << 10
)

type offerRequest struct {
	SDP string `json:"sdp"`
}

type offerResponse struct {
	ID  string `json:"id"`
	SDP string `json:"sdp"`
}

type server struct {
	mu       sync.Mutex
	sessions map[string]*terminal
	nextID   uint64
}

type terminal struct {
	mu        sync.Mutex
	pc        *webrtc.PeerConnection
	dc        *webrtc.DataChannel
	pty       *os.File
	cmd       *exec.Cmd
	closed    bool
	timeout   *time.Timer
	server    *server
	session   string
	closeOnce sync.Once
}

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "loopback listen address")
	flag.Parse()

	host, _, err := net.SplitHostPort(*listen)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		fatal(errors.New("spike server must listen on a loopback address"))
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fatal(err)
	}

	s := &server{sessions: make(map[string]*terminal)}
	httpServer := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       15 * time.Second,
	}

	fmt.Printf("http://%s\n", listener.Addr())
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal(err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	s.closeAll()
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, "<!doctype html><title>Hopty spike</title>")
	})
	mux.HandleFunc("POST /offer", s.handleOffer)
	mux.HandleFunc("POST /sessions/{id}/offer", s.handleReoffer)
	mux.HandleFunc("POST /sessions/{id}/force-disconnect", s.handleForceDisconnect)
	return mux
}

func (s *server) handleOffer(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSignalBytes)
	defer r.Body.Close()

	var request offerRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.SDP == "" {
		http.Error(w, "invalid offer", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid offer", http.StatusBadRequest)
		return
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	terminal := &terminal{pc: pc, server: s}
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != dataChannelLabel || terminal.dataChannel(dc) != nil {
			_ = dc.Close()
			return
		}
		dc.OnOpen(func() {
			if err := terminal.startPTY(); err != nil {
				terminal.close("pty_start_failed")
			}
		})
		dc.OnMessage(terminal.handleMessage)
	})
	pc.OnConnectionStateChange(terminal.handleConnectionState)

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: request.SDP}); err != nil {
		terminal.close("invalid_offer")
		http.Error(w, "invalid offer", http.StatusBadRequest)
		return
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		terminal.close("answer_failed")
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		terminal.close("answer_failed")
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	select {
	case <-gatherComplete:
	case <-r.Context().Done():
		terminal.close("request_cancelled")
		return
	case <-time.After(5 * time.Second):
		terminal.close("gather_timeout")
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	s.mu.Lock()
	s.nextID++
	terminal.session = fmt.Sprintf("%d", s.nextID)
	s.sessions[terminal.session] = terminal
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(offerResponse{ID: terminal.session, SDP: pc.LocalDescription().SDP})
}

func (s *server) handleReoffer(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	terminal := s.sessions[r.PathValue("id")]
	s.mu.Unlock()
	if terminal == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxSignalBytes)
	defer r.Body.Close()
	var request offerRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.SDP == "" {
		http.Error(w, "invalid offer", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid offer", http.StatusBadRequest)
		return
	}

	terminal.mu.Lock()
	pc := terminal.pc
	closed := terminal.closed
	terminal.mu.Unlock()
	if closed || pc == nil || pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: request.SDP}) != nil {
		http.Error(w, "invalid offer", http.StatusBadRequest)
		return
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	select {
	case <-gatherComplete:
	case <-r.Context().Done():
		return
	case <-time.After(5 * time.Second):
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(offerResponse{ID: terminal.session, SDP: pc.LocalDescription().SDP})
}

func (s *server) handleForceDisconnect(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	terminal := s.sessions[r.PathValue("id")]
	s.mu.Unlock()
	if terminal == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	terminal.handleConnectionState(webrtc.PeerConnectionStateDisconnected)
	w.WriteHeader(http.StatusNoContent)
}

func (t *terminal) dataChannel(dc *webrtc.DataChannel) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dc != nil || t.closed {
		return errors.New("data channel unavailable")
	}
	t.dc = dc
	return nil
}

func (t *terminal) startPTY() error {
	t.mu.Lock()
	if t.closed || t.pty != nil {
		t.mu.Unlock()
		return errors.New("terminal unavailable")
	}
	cmd := exec.Command("/bin/sh")
	ptmx, err := pty.Start(cmd)
	if err == nil {
		t.cmd = cmd
		t.pty = ptmx
	}
	t.mu.Unlock()
	if err != nil {
		return err
	}

	go t.copyOutput(ptmx)
	go func() {
		err := cmd.Wait()
		t.sendExit(err)
		time.Sleep(100 * time.Millisecond)
		t.close("shell_exit")
	}()
	return nil
}

func (t *terminal) copyOutput(ptmx *os.File) {
	buffer := make([]byte, maxFrameBytes-1)
	for {
		n, err := ptmx.Read(buffer)
		if n > 0 {
			frame := append([]byte{0x01}, buffer[:n]...)
			t.mu.Lock()
			dc := t.dc
			closed := t.closed
			t.mu.Unlock()
			if !closed && dc != nil {
				_ = dc.Send(frame)
			}
		}
		if err != nil {
			return
		}
	}
}

func (t *terminal) handleMessage(message webrtc.DataChannelMessage) {
	if message.IsString || len(message.Data) == 0 || len(message.Data) > maxFrameBytes {
		t.close("invalid_frame")
		return
	}
	switch message.Data[0] {
	case 0x01:
		t.mu.Lock()
		ptmx := t.pty
		closed := t.closed
		t.mu.Unlock()
		if closed || ptmx == nil {
			t.close("terminal_unavailable")
			return
		}
		if _, err := ptmx.Write(message.Data[1:]); err != nil {
			t.close("pty_write_failed")
		}
	case 0x02:
		if len(message.Data) != 5 {
			t.close("invalid_resize")
			return
		}
		columns := binary.BigEndian.Uint16(message.Data[1:3])
		rows := binary.BigEndian.Uint16(message.Data[3:5])
		if columns == 0 || rows == 0 || columns > 1000 || rows > 1000 {
			t.close("invalid_resize")
			return
		}
		t.mu.Lock()
		ptmx := t.pty
		t.mu.Unlock()
		if ptmx == nil || pty.Setsize(ptmx, &pty.Winsize{Cols: columns, Rows: rows}) != nil {
			t.close("resize_failed")
		}
	default:
		t.close("invalid_frame")
	}
}

func (t *terminal) handleConnectionState(state webrtc.PeerConnectionState) {
	switch state {
	case webrtc.PeerConnectionStateDisconnected:
		t.mu.Lock()
		if t.timeout == nil && !t.closed {
			t.timeout = time.AfterFunc(10*time.Second, func() { t.close("recovery_timeout") })
		}
		t.mu.Unlock()
	case webrtc.PeerConnectionStateConnected:
		t.mu.Lock()
		if t.timeout != nil {
			t.timeout.Stop()
			t.timeout = nil
		}
		t.mu.Unlock()
	case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
		t.close("channel_closed")
	}
}

func (t *terminal) sendExit(waitErr error) {
	status := int32(0)
	if waitErr != nil {
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			status = int32(exitError.ExitCode())
		} else {
			status = -1
		}
	}
	frame := make([]byte, 6)
	frame[0] = 0x03
	binary.BigEndian.PutUint32(frame[1:5], uint32(status))
	frame[5] = 0x01
	t.mu.Lock()
	dc := t.dc
	closed := t.closed
	t.mu.Unlock()
	if !closed && dc != nil {
		_ = dc.Send(frame)
	}
}

func (t *terminal) close(_ string) {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		if t.timeout != nil {
			t.timeout.Stop()
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
		t.server.mu.Lock()
		delete(t.server.sessions, t.session)
		t.server.mu.Unlock()
	})
}

func (s *server) closeAll() {
	s.mu.Lock()
	terminals := make([]*terminal, 0, len(s.sessions))
	for _, terminal := range s.sessions {
		terminals = append(terminals, terminal)
	}
	s.mu.Unlock()
	for _, terminal := range terminals {
		terminal.close("server_shutdown")
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "hopty-spike:", err)
	os.Exit(1)
}
