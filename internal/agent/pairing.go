package agent

import (
	"os"
	"os/user"
	"strings"

	"github.com/marcelcases/hopty-agent/internal/control"
)

func pairingIdentity() control.PairingCreate {
	identity := control.PairingCreate{}
	if current, err := user.Current(); err == nil {
		identity.Username = strings.TrimSpace(current.Username)
	}
	if hostname, err := os.Hostname(); err == nil {
		identity.Hostname = strings.TrimSpace(hostname)
	}
	return identity
}
