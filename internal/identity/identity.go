// Package identity owns the agent's local Ed25519 identity key.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const keyFile = "identity.key"

type Identity struct {
	PrivateKey ed25519.PrivateKey
	PublicKey  ed25519.PublicKey
}

func LoadOrCreate(directory string) (Identity, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Identity{}, err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return Identity{}, err
	}
	path := filepath.Join(directory, keyFile)
	identity, err := load(path)
	if err == nil {
		return identity, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Identity{}, err
	}
	return create(path)
}

func load(path string) (Identity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Identity{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return Identity{}, fmt.Errorf("%s must be a mode 0600 regular file", path)
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, err
	}
	if len(key) != ed25519.PrivateKeySize {
		return Identity{}, fmt.Errorf("%s has an invalid key length", path)
	}
	privateKey := ed25519.PrivateKey(key)
	return Identity{PrivateKey: privateKey, PublicKey: privateKey.Public().(ed25519.PublicKey)}, nil
}

func create(path string) (Identity, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return load(path)
	}
	if err != nil {
		return Identity{}, err
	}
	_, writeErr := file.Write(privateKey)
	closeErr := file.Close()
	if writeErr != nil {
		return Identity{}, writeErr
	}
	if closeErr != nil {
		return Identity{}, closeErr
	}
	return Identity{PrivateKey: privateKey, PublicKey: publicKey}, nil
}

func Sign(privateKey ed25519.PrivateKey, message []byte) []byte {
	return ed25519.Sign(privateKey, message)
}

func Verify(publicKey ed25519.PublicKey, message, signature []byte) bool {
	return ed25519.Verify(publicKey, message, signature)
}
