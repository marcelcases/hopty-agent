// Package agent owns the local agent daemon lifecycle.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/marcelcases/hopty/internal/config"
	"github.com/marcelcases/hopty/internal/control"
	"github.com/marcelcases/hopty/internal/identity"
	"github.com/marcelcases/hopty/internal/localapi"
	"github.com/marcelcases/hopty/internal/terminal"
)

type Daemon struct {
	home       string
	socketPath string
	listener   *net.UnixListener
	lock       *os.File
	identity   identity.Identity
	serviceURL *url.URL
	stateMu    sync.RWMutex
	connected  bool
	paired     bool
	controlMu  sync.Mutex
	connection *control.Session
	terminals  *terminal.Manager
	waitGroup  sync.WaitGroup
}

func Start(home string) (*Daemon, error) {
	identityValue, err := identity.LoadOrCreate(home)
	if err != nil {
		return nil, err
	}
	var serviceURL *url.URL
	if loaded, err := config.Load(home); err == nil {
		serviceURL = loaded.ServiceURL
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	runDirectory := filepath.Join(home, "run")
	if err := os.MkdirAll(runDirectory, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(runDirectory, 0o700); err != nil {
		return nil, err
	}
	lock, err := lock(filepath.Join(runDirectory, "agent.lock"))
	if err != nil {
		return nil, err
	}
	socketPath := filepath.Join(runDirectory, "agent.sock")
	if err := removeSocket(socketPath); err != nil {
		lock.Close()
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		lock.Close()
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		listener.Close()
		lock.Close()
		return nil, err
	}
	daemon := &Daemon{home: home, socketPath: socketPath, listener: listener, lock: lock, identity: identityValue, serviceURL: serviceURL}
	daemon.terminals = terminal.New(func(ctx context.Context, typ string, payload any) error {
		daemon.controlMu.Lock()
		defer daemon.controlMu.Unlock()
		if daemon.connection == nil {
			return errors.New("agent control connection is unavailable")
		}
		return daemon.connection.Send(ctx, typ, payload)
	})
	return daemon, nil
}

func (d *Daemon) SocketPath() string { return d.socketPath }

func (d *Daemon) Run(ctx context.Context) error {
	if d.serviceURL != nil {
		d.waitGroup.Add(1)
		go func() {
			defer d.waitGroup.Done()
			d.runControl(ctx)
		}()
	}
	go func() {
		<-ctx.Done()
		_ = d.listener.Close()
	}()
	for {
		connection, err := d.listener.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		d.waitGroup.Add(1)
		go func() {
			defer d.waitGroup.Done()
			defer connection.Close()
			_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
			d.handle(connection)
		}()
	}
}

func (d *Daemon) Close() error {
	if d.listener != nil {
		_ = d.listener.Close()
	}
	if d.terminals != nil {
		d.terminals.CloseAll()
	}
	d.waitGroup.Wait()
	_ = os.Remove(d.socketPath)
	if d.lock != nil {
		return d.lock.Close()
	}
	return nil
}

func (d *Daemon) handle(connection *net.UnixConn) {
	request, err := localapi.ReadRequest(connection)
	if err != nil {
		return
	}
	response := localapi.Response{}
	switch request.Command {
	case "status":
		d.stateMu.RLock()
		response.Status = &localapi.Status{Connected: d.connected, Paired: d.paired}
		d.stateMu.RUnlock()
	case "pair":
		response.Pairing, response.Error = d.createPairing()
	case "cancel":
		response.Error = d.cancelPairing()
	case "revoke":
		response.Error = "credential revocation is unavailable"
	default:
		response.Error = "unknown command"
	}
	_ = localapi.WriteResponse(connection, response)
}

func (d *Daemon) createPairing() (*localapi.Pairing, string) {
	d.controlMu.Lock()
	defer d.controlMu.Unlock()
	if d.connection == nil {
		return nil, "agent control connection is unavailable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created, err := control.CreatePairing(ctx, d.connection)
	if err != nil {
		return nil, "agent control connection is unavailable"
	}
	return &localapi.Pairing{URL: created.PairingURL, Code: created.VerificationCode, ExpiresAt: created.ExpiresAt.UTC().Format(time.RFC3339)}, ""
}

func (d *Daemon) cancelPairing() string {
	d.controlMu.Lock()
	defer d.controlMu.Unlock()
	if d.connection == nil {
		return "agent control connection is unavailable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := control.CancelPairing(ctx, d.connection); err != nil {
		return "agent control connection is unavailable"
	}
	return ""
}

func (d *Daemon) runControl(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		ready, connection, err := control.Connect(ctx, d.serviceURL, d.identity.PrivateKey, "dev")
		if err == nil {
			backoff = time.Second
			connectionCtx, stopConnection := context.WithCancel(ctx)
			session := control.NewSession(connection)
			readErr := make(chan error, 1)
			keepaliveDone := make(chan struct{})
			go func() { readErr <- session.Run(connectionCtx) }()
			go func() {
				defer close(keepaliveDone)
				ticker := time.NewTicker(20 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-connectionCtx.Done():
						return
					case <-ticker.C:
						pingCtx, cancel := context.WithTimeout(connectionCtx, 10*time.Second)
						err := session.Ping(pingCtx)
						cancel()
						if err != nil {
							session.CloseNow()
							return
						}
					}
				}
			}()
			d.stateMu.Lock()
			d.connected, d.paired = true, ready.Paired
			d.stateMu.Unlock()
			d.controlMu.Lock()
			d.connection = session
			d.controlMu.Unlock()
			if d.terminals != nil {
				_ = session.Send(connectionCtx, "agent.active_terminals", control.ActiveTerminals{TerminalIDs: d.terminals.ActiveIDs()})
			}
			connected := true
			for connected {
				select {
				case <-ctx.Done():
					connected = false
				case event, ok := <-session.Events():
					if !ok {
						connected = false
					} else {
						d.handleControlEvent(event)
					}
				case <-readErr:
					connected = false
				}
			}
			stopConnection()
			session.CloseNow()
			<-keepaliveDone
			d.controlMu.Lock()
			d.connection = nil
			d.controlMu.Unlock()
		}
		d.stateMu.Lock()
		d.connected = false
		d.stateMu.Unlock()
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (d *Daemon) handleControlEvent(event control.Envelope) {
	if d.terminals == nil {
		return
	}
	switch event.Type {
	case "terminal.open":
		var open control.TerminalOpen
		if json.Unmarshal(event.Payload, &open) == nil {
			_ = d.terminals.Open(open)
		}
	case "terminal.signal":
		var signal control.TerminalSignal
		if json.Unmarshal(event.Payload, &signal) == nil {
			_ = d.terminals.Signal(context.Background(), signal)
		}
	case "terminal.close":
		var signal control.TerminalSignal
		if json.Unmarshal(event.Payload, &signal) == nil {
			_ = d.terminals.Signal(context.Background(), signal)
		}
	}
}

func lock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("agent daemon is already running: %w", err)
	}
	return file, nil
}

func removeSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s is not a socket", path)
	}
	return os.Remove(path)
}
