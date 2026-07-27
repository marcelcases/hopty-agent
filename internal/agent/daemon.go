// Package agent owns the local agent daemon lifecycle.
package agent

import (
	"context"
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
	return &Daemon{home: home, socketPath: socketPath, listener: listener, lock: lock, identity: identityValue, serviceURL: serviceURL}, nil
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
	case "pair", "cancel", "revoke":
		response.Error = "agent control connection is unavailable"
	default:
		response.Error = "unknown command"
	}
	_ = localapi.WriteResponse(connection, response)
}

func (d *Daemon) runControl(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		ready, connection, err := control.Connect(ctx, d.serviceURL, d.identity.PrivateKey, "dev")
		if err == nil {
			d.stateMu.Lock()
			d.connected, d.paired = true, ready.Paired
			d.stateMu.Unlock()
			_, _, err = connection.Reader(ctx)
			connection.CloseNow()
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
