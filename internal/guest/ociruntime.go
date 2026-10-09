//go:build linux

package guest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// Containers and build steps get the environment's CA at ContainerCADir: OCIRuntime
// bind-mounts ConfigDir there and points TLS clients at it. Containers have a tmpfs at /dev,
// so the mountpoint never ends up in a committed or built image.
const (
	ContainerCADir = "/dev/sandbox-studio"
	caBundle       = ConfigDir + "/ca-bundle.crt" // the guest's system roots, which include the CA
	realRunc       = "/usr/bin/runc"
)

// containerEnv points common TLS clients at the CA. A variable the image or `docker run -e`
// already sets wins.
var containerEnv = []string{
	"SSL_CERT_FILE=" + ContainerCADir + "/ca-bundle.crt",
	"CURL_CA_BUNDLE=" + ContainerCADir + "/ca-bundle.crt",
	"REQUESTS_CA_BUNDLE=" + ContainerCADir + "/ca-bundle.crt",
	"GIT_SSL_CAINFO=" + ContainerCADir + "/ca-bundle.crt",
	"NODE_EXTRA_CA_CERTS=" + ContainerCADir + "/ca.crt",
}

// OCIRuntime runs as runc in a sandbox: the base image puts a script ahead of the real runc
// on PATH, where containerd and BuildKit look for it. It adds the environment's CA to every
// container it creates, then hands over to runc with args unchanged. A container whose
// spec can't be amended still starts, just without the CA.
func OCIRuntime(args []string) error {
	if bundle, ok := createdBundle(args); ok {
		if _, err := os.Stat(caBundle); err == nil {
			if err := addCA(filepath.Join(bundle, "config.json")); err != nil {
				fmt.Fprintf(os.Stderr, "studio-agent: not adding the Sandbox Studio CA to the container: %v\n", err)
			}
		}
	}
	return syscall.Exec(realRunc, append([]string{"runc"}, args...), os.Environ())
}

// createdBundle finds the bundle directory of a runc "create" or "run" invocation, whose
// global flags come first: runc [global flags] create [flags] <id>.
func createdBundle(args []string) (string, bool) {
	i := slices.IndexFunc(args, func(a string) bool { return a == "create" || a == "run" })
	if i < 0 {
		return "", false
	}
	for j := i + 1; j < len(args); j++ {
		switch a := args[j]; {
		case (a == "--bundle" || a == "-b") && j+1 < len(args):
			return args[j+1], true
		case strings.HasPrefix(a, "--bundle="):
			return strings.TrimPrefix(a, "--bundle="), true
		}
	}
	dir, err := os.Getwd() // runc's default bundle
	return dir, err == nil
}

// addCA amends an OCI runtime spec with a read-only bind mount of the CA and the
// environment variables pointing at it. Unknown fields are kept as they are.
func addCA(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var spec map[string]json.RawMessage
	if err := json.Unmarshal(raw, &spec); err != nil {
		return err
	}

	var mounts []map[string]any
	if m, ok := spec["mounts"]; ok {
		if err := json.Unmarshal(m, &mounts); err != nil {
			return fmt.Errorf("mounts: %w", err)
		}
	}
	if slices.ContainsFunc(mounts, func(m map[string]any) bool { return m["destination"] == ContainerCADir }) {
		return nil // already amended, or the user mounted something there
	}
	mounts = append(mounts, map[string]any{
		"destination": ContainerCADir,
		"type":        "bind",
		"source":      ConfigDir,
		"options":     []string{"rbind", "ro", "nosuid", "nodev", "noexec"},
	})

	var process map[string]json.RawMessage
	if p, ok := spec["process"]; ok {
		if err := json.Unmarshal(p, &process); err != nil {
			return fmt.Errorf("process: %w", err)
		}
	}
	var env []string
	if e, ok := process["env"]; ok {
		if err := json.Unmarshal(e, &env); err != nil {
			return fmt.Errorf("process.env: %w", err)
		}
	}
	for _, kv := range containerEnv {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, name+"=") }) {
			env = append(env, kv)
		}
	}

	if spec["mounts"], err = json.Marshal(mounts); err != nil {
		return err
	}
	if process != nil {
		if process["env"], err = json.Marshal(env); err != nil {
			return err
		}
		if spec["process"], err = json.Marshal(process); err != nil {
			return err
		}
	}
	out, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp := path + ".studio"
	if err := os.WriteFile(tmp, out, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
