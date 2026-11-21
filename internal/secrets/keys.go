package secrets

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

// ErrNoKey is returned by a KeyStore that holds no master key.
var ErrNoKey = errors.New("no vault key stored")

// KeyStore holds the master key outside the database, so a copy of studio.db alone cannot
// open the secrets in it.
type KeyStore interface {
	// Name identifies the backend in the database: "keychain" or "file".
	Name() string
	// Get returns the key, or ErrNoKey if none is stored.
	Get() ([]byte, error)
	// Set stores the key.
	Set(key []byte) error
}

// Backend names, as recorded in the database.
const (
	backendKeychain = "keychain"
	backendFile     = "file"
)

const keychainService = "Sandbox Studio"

// keychain stores the key in the OS keychain: macOS Keychain, Windows Credential Manager or
// the Secret Service on Linux.
type keychain struct{ account string }

func (keychain) Name() string { return backendKeychain }

// keychainWait bounds a keychain read. On Linux a locked keychain is asked to unlock with a
// prompt, whose answer never comes when its daemon can't show one. A var so tests can shorten it.
var keychainWait = time.Minute

// keychainRead is a read of the keychain that may outlive the Get that started it: one that
// didn't answer in time still waits on its prompt, or on macOS its security process.
type keychainRead struct {
	done chan struct{}
	s    string
	err  error
}

var (
	readMu   sync.Mutex
	inFlight = map[string]*keychainRead{} // by account
)

func (k keychain) Get() ([]byte, error) {
	// A read still waiting is waited on again rather than another started, so that a prompt
	// nobody answers doesn't pile up more of them; but one that waited while the keychain
	// was locked can't see it unlock, so a keychain known to be open is read afresh.
	locked, ok := KeychainLocked()
	readMu.Lock()
	r := inFlight[k.account]
	if r == nil || ok && !locked {
		r = &keychainRead{done: make(chan struct{})}
		inFlight[k.account] = r
		go func() {
			r.s, r.err = keyring.Get(keychainService, k.account)
			readMu.Lock()
			if inFlight[k.account] == r {
				delete(inFlight, k.account)
			}
			readMu.Unlock()
			close(r.done)
		}()
	}
	readMu.Unlock()
	select {
	case <-r.done:
	case <-time.After(keychainWait):
		return nil, fmt.Errorf("the keychain didn't answer within %s", keychainWait)
	}
	s, err := r.s, r.err
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNoKey
	}
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(s)
}

func (k keychain) Set(key []byte) error {
	return keyring.Set(keychainService, k.account, base64.StdEncoding.EncodeToString(key))
}

// keyFile keeps the base64-encoded key in a file that only its owner can read.
type keyFile struct{ path string }

func (keyFile) Name() string { return backendFile }

func (f keyFile) Get() ([]byte, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoKey
	}
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(string(b))
}

// Set writes through a temporary file so an interrupted write never leaves a truncated key.
// CreateTemp makes the file readable by its owner only.
func (f keyFile) Set(key []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(f.path), filepath.Base(f.path)+".*")
	if err != nil {
		return err
	}
	_, err = tmp.WriteString(base64.StdEncoding.EncodeToString(key))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), f.path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
