// Package sshconfig lets any SSH client reach Studio's sandboxes (docs/sandboxes.md#ssh-access).
// Studio keeps an OpenSSH config file in its data directory with a host per sandbox,
// my-app.studio, and one Host *.studio block whose ProxyCommand is `studio ssh-proxy`. The
// user's ~/.ssh/config includes that file while SSH access is on, so ssh, scp, rsync, VS Code
// Remote-SSH, JetBrains Gateway and Zed list and reach the sandboxes by name.
//
// The client key, the host key, the authorized keys and the known hosts are Studio's own, in
// the same directory: no key of the user's is needed or touched, and a sandbox's host key
// stays the same across its VM generations.
package sshconfig

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Suffix ends every host name Studio's config file knows.
const Suffix = ".studio"

// The files in a Service's Dir.
const (
	configFile     = "config"
	clientKeyFile  = "id_ed25519"
	hostKeyFile    = "host_ed25519"
	authorizedFile = "authorized_keys"
	knownHostsFile = "known_hosts"
)

// includeComment marks the Include line Enable adds to the user's config.
const includeComment = "# Sandbox Studio's sandboxes; `studio ssh disable` removes these two lines."

// Status describes SSH access.
type Status struct {
	Enabled    bool   `json:"enabled" doc:"Whether the user's SSH config includes Studio's"`
	UserConfig string `json:"userConfig" doc:"The user's SSH config file, such as ~/.ssh/config"`
	Config     string `json:"config" doc:"Studio's SSH config file, which lists a host per sandbox"`
}

// Service manages the SSH files of one installation.
type Service struct {
	Dir        string // Studio's SSH directory, <data>/ssh
	UserConfig string // the user's SSH config, ~/.ssh/config
	Exe        string // the Studio binary the ProxyCommand runs
	Addr       string // the address of the Studio the ProxyCommand asks
	// Data and MSBHome are the installation's data directory and, if set, microsandbox's
	// MSB_HOME. The ProxyCommand names them because ssh runs it without Studio's environment.
	Data    string
	MSBHome string
}

// Paths of the files in Dir.
func (s *Service) Config() string         { return filepath.Join(s.Dir, configFile) }
func (s *Service) HostKey() string        { return filepath.Join(s.Dir, hostKeyFile) }
func (s *Service) AuthorizedKeys() string { return filepath.Join(s.Dir, authorizedFile) }

// DefaultUserConfig is ~/.ssh/config.
func DefaultUserConfig() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// Host is one sandbox in the config file.
type Host struct {
	Alias string // the host name clients use, my-app.studio
	ID    string // the sandbox's ID
}

// Hosts names the sandboxes: name.studio, or name-<id>.studio for a name that sandboxes
// of several environments have.
func Hosts(sbs []store.Sandbox) []Host {
	count := map[string]int{}
	for _, sb := range sbs {
		count[sb.Name]++
	}
	out := make([]Host, 0, len(sbs))
	for _, sb := range sbs {
		alias := sb.Name
		if count[sb.Name] > 1 {
			alias += "-" + sb.ID
		}
		out = append(out, Host{Alias: alias + Suffix, ID: sb.ID})
	}
	slices.SortFunc(out, func(a, b Host) int { return strings.Compare(a.Alias, b.Alias) })
	return out
}

// Ensure creates Dir and the keys, the authorized keys and the known hosts it lacks.
func (s *Service) Ensure() error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	client, err := keyPair(filepath.Join(s.Dir, clientKeyFile), "sandbox-studio")
	if err != nil {
		return err
	}
	host, err := keyPair(s.HostKey(), "sandbox-studio host")
	if err != nil {
		return err
	}
	if err := writeIfChanged(s.AuthorizedKeys(), ssh.MarshalAuthorizedKey(client), 0o600); err != nil {
		return err
	}
	known := "*" + Suffix + " " + string(ssh.MarshalAuthorizedKey(host))
	return writeIfChanged(filepath.Join(s.Dir, knownHostsFile), []byte(known), 0o600)
}

// keyPair reads the ed25519 private key at path, creating it and its .pub on first use.
func keyPair(path, comment string) (ssh.PublicKey, error) {
	if pemBytes, err := os.ReadFile(path); err == nil {
		signer, err := ssh.ParsePrivateKey(pemBytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return signer.PublicKey(), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	if err := writeIfChanged(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	line := bytes.TrimSuffix(ssh.MarshalAuthorizedKey(sshPub), []byte("\n"))
	return sshPub, writeIfChanged(path+".pub", append(line, []byte(" "+comment+"\n")...), 0o644)
}

// Render is the config file for hosts.
func (s *Service) Render(hosts []Host) []byte {
	var b bytes.Buffer
	b.WriteString("# Sandbox Studio writes this file and rewrites it when sandboxes change, so edits\n")
	b.WriteString("# here are lost. Each sandbox is a host, and any <name or ID>.studio reaches one.\n")
	for _, h := range hosts {
		fmt.Fprintf(&b, "\nHost %s\n    HostName %s%s\n", h.Alias, h.ID, Suffix)
	}
	fmt.Fprintf(&b, "\nHost *%s\n", Suffix)
	b.WriteString("    User agent\n")
	fmt.Fprintf(&b, "    ProxyCommand %s\n", s.proxyCommand())
	fmt.Fprintf(&b, "    IdentityFile %s\n", quote(filepath.Join(s.Dir, clientKeyFile)))
	b.WriteString("    IdentitiesOnly yes\n")
	fmt.Fprintf(&b, "    UserKnownHostsFile %s\n", quote(filepath.Join(s.Dir, knownHostsFile)))
	b.WriteString("    StrictHostKeyChecking yes\n")
	return b.Bytes()
}

// proxyCommand runs `studio ssh-proxy` for the host ssh connects to, %h.
func (s *Service) proxyCommand() string {
	cmd := quote(s.Exe) + " -addr " + s.Addr + " ssh-proxy -data " + quote(s.Data)
	if s.MSBHome != "" {
		cmd += " -msb-home " + quote(s.MSBHome)
	}
	return cmd + " %h"
}

// Write writes the config file for hosts, if it changed.
func (s *Service) Write(hosts []Host) error {
	return writeIfChanged(s.Config(), s.Render(hosts), 0o600)
}

// quote puts a path in double quotes, which both ssh's config parser and the shell that
// runs the ProxyCommand read as one word.
func quote(path string) string {
	return `"` + path + `"`
}

// includeLine is the line Enable adds to the user's config.
func (s *Service) includeLine() string { return "Include " + quote(s.Config()) }

// Status reports whether the user's config includes Studio's.
func (s *Service) Status() (Status, error) {
	st := Status{UserConfig: s.UserConfig, Config: s.Config()}
	data, err := os.ReadFile(s.UserConfig)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.Enabled = slices.Contains(lines(data), s.includeLine())
	return st, nil
}

// Enable adds the Include line at the top of the user's config, where it applies to every
// host: an Include after a Host line would apply to that host only. It creates the file,
// and ~/.ssh, when there is none.
func (s *Service) Enable() (Status, error) {
	st, err := s.Status()
	if err != nil || st.Enabled {
		return st, err
	}
	data, err := os.ReadFile(s.UserConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return st, err
	}
	if err := os.MkdirAll(filepath.Dir(s.UserConfig), 0o700); err != nil {
		return st, err
	}
	head := includeComment + "\n" + s.includeLine() + "\n"
	if len(data) > 0 {
		head += "\n"
	}
	if err := writeKeepingMode(s.UserConfig, append([]byte(head), data...)); err != nil {
		return st, &WriteError{Path: s.UserConfig, Line: s.includeLine(), Add: true, Err: err}
	}
	st.Enabled = true
	return st, nil
}

// Disable removes the Include line, and the comment above it, from the user's config.
func (s *Service) Disable() (Status, error) {
	st, err := s.Status()
	if err != nil || !st.Enabled {
		return st, err
	}
	data, err := os.ReadFile(s.UserConfig)
	if err != nil {
		return st, err
	}
	in := lines(data)
	var out []string
	for i, line := range in {
		if line == s.includeLine() || (line == includeComment && i+1 < len(in) && in[i+1] == s.includeLine()) {
			continue
		}
		out = append(out, line)
	}
	// Drop the blank line Enable left between its lines and the user's.
	if len(out) > 0 && out[0] == "" && len(in) > 0 && in[0] == includeComment {
		out = out[1:]
	}
	text := strings.Join(out, "\n")
	if len(out) > 0 {
		text += "\n"
	}
	if err := writeKeepingMode(s.UserConfig, []byte(text)); err != nil {
		return st, &WriteError{Path: s.UserConfig, Line: s.includeLine(), Err: err}
	}
	st.Enabled = false
	return st, nil
}

// WriteError means the user's config can't be changed, such as one a dotfile manager such
// as home-manager keeps read-only. The user adds or removes Line there instead.
type WriteError struct {
	Path string
	Line string
	Add  bool
	Err  error
}

func (e *WriteError) Error() string {
	if e.Add {
		return fmt.Sprintf("%s can't be changed (%v). Add this line at its top yourself, or where your dotfiles make it: %s", e.Path, e.Err, e.Line)
	}
	return fmt.Sprintf("%s can't be changed (%v). Remove this line yourself: %s", e.Path, e.Err, e.Line)
}

func (e *WriteError) Unwrap() error { return e.Err }

// lines splits a config file, ignoring a final newline and Windows line ends.
func lines(data []byte) []string {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// writeIfChanged writes data to path through a temporary file, unless path has it already.
func writeIfChanged(path string, data []byte, mode os.FileMode) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	return writeFile(path, data, mode)
}

// writeKeepingMode writes the user's config, keeping its permissions; a new one is 0600,
// as ssh wants. A symlinked config, as dotfile managers keep it, is written where it points.
func writeKeepingMode(path string, data []byte) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return writeFile(path, data, mode)
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
