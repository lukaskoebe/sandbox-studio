// Command transfer-speed measures the ways bulk data can leave and enter a VM, to choose the
// transport of a disk resize's copy: msb exec stdout and stdin (what Studio uses today), a
// shared host directory, and TCP through a SOCKS5 proxy (the way to Studio's gateway). It also
// times studio-agent's export and import of a real tree, to see whether the transport or the
// archive is the bottleneck.
//
// Point MSB_HOME at a scratch directory. It boots one alpine VM with the guest agent bind
// mounted, a host tree mounted read-only at /workspace as the data to export, an owned disk at
// /home/agent to import into, and a shared directory at /share:
//
//	MSB_HOME=$(mktemp -d -p ~/git) transfer-speed -agent internal/agentbin/dist/studio-agent-linux-amd64 -tree web/node_modules
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	studioruntime "github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

const (
	sinkPort   = 9001 // the proxy counts what the guest sends here
	sourcePort = 9002 // and sends zeros from here
)

func main() {
	log.SetFlags(log.Ltime)
	agent := flag.String("agent", "", "the linux studio-agent binary")
	tree := flag.String("tree", "", "a host directory to export, mounted read-only")
	size := flag.Int64("bytes", 1<<30, "bytes per raw stream")
	cpus := flag.Int("cpus", 4, "the VM's CPUs")
	archiveOnly := flag.Bool("archive-only", false, "skip the raw streams")
	flag.Parse()
	if os.Getenv("MSB_HOME") == "" || *agent == "" || *tree == "" {
		log.Fatal("set MSB_HOME to a scratch directory, and pass -agent and -tree")
	}
	if err := run(*agent, *tree, *size, uint8(*cpus), *archiveOnly); err != nil {
		log.Fatal("FAIL: ", err)
	}
}

func run(agent, tree string, size int64, cpus uint8, archiveOnly bool) (retErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if _, err := msb.EnsureRuntime(ctx, msb.RuntimeConfig{}, msb.InstallOptions{}); err != nil {
		return fmt.Errorf("install msb: %w", err)
	}
	scratch := os.Getenv("MSB_HOME")
	guestDir := filepath.Join(scratch, "guest")
	share := filepath.Join(scratch, "share")
	for _, d := range []string{filepath.Join(guestDir, "bin"), share} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	raw, err := os.ReadFile(agent)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(guestDir, "bin", "studio-agent"), raw, 0o755); err != nil {
		return err
	}
	tree, err = filepath.Abs(tree)
	if err != nil {
		return err
	}

	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer proxy.Close()
	var received atomic.Int64
	sinkDone := make(chan time.Time, 1)
	var expect atomic.Int64
	expect.Store(size)
	go serveSOCKS(proxy, &expect, &received, sinkDone)

	owned := studioruntime.OwnedVM{Name: "spike-transfer-speed", Labels: map[string]string{"studio.kind": "spike", "studio.spike": "transfer-speed"}}
	rt := studioruntime.New(studioruntime.Options{Image: "alpine:3.20"})
	_ = msb.RemoveSandbox(ctx, owned.Name)
	log.Printf("creating %s with %d CPUs", owned.Name, cpus)
	vm, err := msb.CreateSandbox(ctx, owned.Name,
		msb.WithImage("alpine:3.20"), msb.WithCPUs(cpus), msb.WithMemory(2048), msb.WithMaxMemory(2048),
		msb.WithMounts(map[string]msb.MountConfig{
			"/workspace":  msb.Mount.Bind(tree, msb.MountOptions{Readonly: true}),
			"/opt/studio": msb.Mount.Bind(guestDir, msb.MountOptions{Readonly: true}),
			"/share":      msb.Mount.Bind(share, msb.MountOptions{}),
			"/home/agent": msb.Mount.Owned(msb.OwnedVolumeOptions{Kind: msb.VolumeKindDisk, SizeMiB: 4096}),
		}),
		msb.WithNetwork(&msb.NetworkConfig{
			Rules: []msb.PolicyRule{{Action: msb.PolicyActionAllow, Direction: msb.PolicyDirectionEgress, Destination: "public",
				Protocols: []msb.PolicyProtocol{msb.PolicyProtocolTCP}}},
			DefaultEgress: msb.PolicyActionDeny, DefaultIngress: msb.PolicyActionAllow,
		}),
		msb.WithProxy(msb.SOCKS5Proxy(proxy.Addr().String())),
		msb.WithLabels(owned.Labels), msb.WithDetached())
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		retErr = errors.Join(retErr, rt.RemoveOwned(cleanup, owned))
	}()
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if err := vm.Detach(ctx); err != nil {
		return err
	}

	sh := func(script string, stdin io.Reader, stdout io.Writer) (time.Duration, error) {
		var errOut bytes.Buffer
		if stdout == nil {
			stdout = &errOut
		}
		start := time.Now()
		res, err := rt.Run(ctx, owned, studioruntime.RunCommand{Path: "/bin/sh", Args: []string{"-c", script},
			User: "root", Cwd: "/", Stdin: stdin, Stdout: stdout, Timeout: 20 * time.Minute}, nil)
		took := time.Since(start)
		if err == nil && res.ExitCode != 0 {
			err = fmt.Errorf("%q exited %d: %s", script, res.ExitCode, errOut.String())
		}
		return took, err
	}
	shEnv := func(script string, env map[string]string, stdout io.Writer) (time.Duration, error) {
		start := time.Now()
		res, err := rt.Run(ctx, owned, studioruntime.RunCommand{Path: "/bin/sh", Args: []string{"-c", script + " 2>&1"},
			User: "root", Cwd: "/", Env: env, Stdout: stdout, Timeout: 20 * time.Minute}, nil)
		if err == nil && res.ExitCode != 0 {
			err = fmt.Errorf("%q exited %d", script, res.ExitCode)
		}
		return time.Since(start), err
	}
	report := func(what string, n int64, took time.Duration) {
		fmt.Printf("%-44s %8.1f MB in %7.2fs = %7.1f MB/s\n", what, float64(n)/1e6, took.Seconds(), float64(n)/took.Seconds()/1e6)
	}

	if !archiveOnly {
		if err := rawStreams(sh, report, size, share, &received, sinkDone); err != nil {
			return err
		}
	}
	return archives(sh, shEnv, report, tree, share, &expect, &received, sinkDone)
}

type shFunc func(script string, stdin io.Reader, stdout io.Writer) (time.Duration, error)
type shEnvFunc func(script string, env map[string]string, stdout io.Writer) (time.Duration, error)
type reportFunc func(what string, n int64, took time.Duration)

// raw times streams of zeros through each transport.
func rawStreams(sh shFunc, report reportFunc, size int64, share string, received *atomic.Int64, sinkDone <-chan time.Time) error {
	mib := size >> 20
	// The disk the import writes to, for reference.
	took, err := sh(fmt.Sprintf("dd if=/dev/zero of=/home/agent/z bs=1M count=%d conv=fsync 2>/dev/null && rm /home/agent/z", mib), nil, nil)
	if err != nil {
		return err
	}
	report("guest disk write (dd, fsync)", size, took)

	// msb exec, both ways.
	var out countWriter
	if took, err = sh(fmt.Sprintf("head -c %d /dev/zero", size), nil, &out); err != nil {
		return err
	}
	report("exec stdout, guest to host", out.n, took)
	if took, err = sh("cat > /dev/null", io.LimitReader(zeros{}, size), nil); err != nil {
		return err
	}
	report("exec stdin, host to guest", size, took)

	// The shared directory, both ways.
	if took, err = sh(fmt.Sprintf("dd if=/dev/zero of=/share/out bs=1M count=%d conv=fsync 2>/dev/null", mib), nil, nil); err != nil {
		return err
	}
	report("shared dir, guest writes", size, took)
	if err := os.Remove(filepath.Join(share, "out")); err != nil {
		return err
	}
	if err := writeZeros(filepath.Join(share, "in"), size); err != nil {
		return err
	}
	if took, err = sh("cat /share/in > /dev/null", nil, nil); err != nil {
		return err
	}
	report("shared dir, guest reads", size, took)
	if err := os.Remove(filepath.Join(share, "in")); err != nil {
		return err
	}

	// TCP through the SOCKS5 proxy, both ways. 1.1.1.1 never sees it: the proxy answers.
	start := time.Now()
	if _, err = sh(fmt.Sprintf("head -c %d /dev/zero | nc 1.1.1.1 %d", size, sinkPort), nil, nil); err != nil {
		return err
	}
	select {
	case end := <-sinkDone:
		report("tcp via socks5, guest to host", received.Load(), end.Sub(start))
	case <-time.After(10 * time.Second):
		return fmt.Errorf("the sink got %d of %d bytes", received.Load(), size)
	}
	var in countWriter
	if took, err = sh(fmt.Sprintf("nc 1.1.1.1 %d | wc -c", sourcePort), nil, &in); err != nil {
		return err
	}
	report("tcp via socks5, host to guest", size, took)
	return nil
}

// archives times studio-agent's export and import of tree.
func archives(sh shFunc, shEnv shEnvFunc, report reportFunc, tree, share string, expect, received *atomic.Int64, sinkDone <-chan time.Time) error {

	// The archive itself: export a real tree, in the guest and over exec.
	var archive bytes.Buffer
	took, err := sh("/opt/studio/bin/studio-agent workspace-export --zstd", nil, &archive)
	if err != nil {
		return err
	}
	treeBytes, err := du(tree)
	if err != nil {
		return err
	}
	fmt.Printf("tree %s: %.1f MB of files, %.1f MB as zstd tar\n", tree, float64(treeBytes)/1e6, float64(archive.Len())/1e6)
	report("workspace-export --zstd over exec (tree MB)", treeBytes, took)
	if took, err = sh("/opt/studio/bin/studio-agent workspace-export --zstd > /dev/null", nil, nil); err != nil {
		return err
	}
	report("workspace-export --zstd in guest (tree MB)", treeBytes, took)
	if took, err = sh("/opt/studio/bin/studio-agent workspace-export > /dev/null", nil, nil); err != nil {
		return err
	}
	report("workspace-export gzip in guest (tree MB)", treeBytes, took)
	if took, err = sh("tar -C /workspace -cf - . > /dev/null", nil, nil); err != nil {
		return err
	}
	report("busybox tar, no compression (tree MB)", treeBytes, took)

	// And imported, over exec and from the shared directory.
	if took, err = sh("/opt/studio/bin/studio-agent home-import", bytes.NewReader(archive.Bytes()), nil); err != nil {
		return err
	}
	report("home-import over exec (tree MB)", treeBytes, took)
	if _, err = sh("find /home/agent -mindepth 1 -maxdepth 1 ! -name lost+found -exec rm -rf {} +", nil, nil); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(share, "a.tar.zst"), archive.Bytes(), 0o644); err != nil {
		return err
	}
	if took, err = sh("/opt/studio/bin/studio-agent home-import < /share/a.tar.zst", nil, nil); err != nil {
		return err
	}
	report("home-import from shared dir (tree MB)", treeBytes, took)
	if err := os.Remove(filepath.Join(share, "a.tar.zst")); err != nil {
		return err
	}

	// The same tree exported from the owned ext4 disk, as a real source is.
	if _, err = sh("sync && echo 3 > /proc/sys/vm/drop_caches", nil, nil); err != nil {
		return err
	}
	if took, err = sh("/opt/studio/bin/studio-agent home-export --zstd > /dev/null", nil, nil); err != nil {
		return err
	}
	report("home-export --zstd from ext4, cold (tree MB)", treeBytes, took)
	if took, err = sh("/opt/studio/bin/studio-agent home-export --zstd > /dev/null", nil, nil); err != nil {
		return err
	}
	report("home-export --zstd from ext4, warm (tree MB)", treeBytes, took)
	var out countWriter
	if took, err = sh("/opt/studio/bin/studio-agent home-export --zstd", nil, &out); err != nil {
		return err
	}
	report("home-export --zstd from ext4 over exec", treeBytes, took)
	fmt.Printf("  (%.1f MB on the wire)\n", float64(out.n)/1e6)
	if took, err = sh("tar -C /home/agent -cf - . > /dev/null", nil, nil); err != nil {
		return err
	}
	report("busybox tar from ext4 (tree MB)", treeBytes, took)

	// Export into TCP, end to end, for this tree and for 1 GB that zstd can't shrink.
	viaTCP := func(what string, n int64) error {
		var wire bytes.Buffer
		if _, err := sh("/opt/studio/bin/studio-agent home-export --zstd | wc -c", nil, &wire); err != nil {
			return err
		}
		var size int64
		if _, err := fmt.Sscan(wire.String(), &size); err != nil {
			return err
		}
		expect.Store(size)
		if _, err := sh("sync && echo 3 > /proc/sys/vm/drop_caches", nil, nil); err != nil {
			return err
		}
		start := time.Now()
		if _, err := sh(fmt.Sprintf("/opt/studio/bin/studio-agent home-export --zstd | nc 1.1.1.1 %d", sinkPort), nil, nil); err != nil {
			return err
		}
		select {
		case end := <-sinkDone:
			report(what, n, end.Sub(start))
			fmt.Printf("  (%.1f MB on the wire)\n", float64(received.Load())/1e6)
		case <-time.After(10 * time.Second):
			return fmt.Errorf("the sink got %d of %d bytes", received.Load(), size)
		}
		return nil
	}
	if err := viaTCP("home-export --zstd into tcp, cold (tree MB)", treeBytes); err != nil {
		return err
	}
	if _, err = sh("find /home/agent -mindepth 1 -maxdepth 1 ! -name lost+found -exec rm -rf {} + && head -c 1073741824 /dev/urandom > /home/agent/random", nil, nil); err != nil {
		return err
	}
	if _, err = sh("sync && echo 3 > /proc/sys/vm/drop_caches", nil, nil); err != nil {
		return err
	}
	if took, err = sh("/opt/studio/bin/studio-agent home-export --zstd > /dev/null", nil, nil); err != nil {
		return err
	}
	report("home-export --zstd of 1 GB random, cold", 1<<30, took)
	if err := viaTCP("home-export --zstd of 1 GB random into tcp", 1<<30); err != nil {
		return err
	}

	// What Studio does: the guest agent POSTs the export over TLS, here to 1.1.1.1, which
	// the proxy hands to a local HTTPS server, as the gateway hands transfer.studio.internal
	// to its virtual host.
	if _, err = sh("sync && echo 3 > /proc/sys/vm/drop_caches", nil, nil); err != nil {
		return err
	}
	var uploaded atomic.Int64
	srv, caPEM, err := uploadServer(&uploaded)
	if err != nil {
		return err
	}
	defer srv.Close()
	httpsAddr.Store(srv.Listener.Addr().String())
	start := time.Now()
	var errOut bytes.Buffer
	if _, err := shEnv("/opt/studio/bin/studio-agent home-export --zstd", map[string]string{
		agentproto.TransferURLEnv: "https://1.1.1.1/upload", agentproto.TransferCAEnv: string(caPEM),
	}, &errOut); err != nil {
		return fmt.Errorf("%w: %s", err, errOut.String())
	}
	report("home-export --zstd of 1 GB random, uploaded", 1<<30, time.Since(start))
	fmt.Printf("  (%.1f MB uploaded)\n", float64(uploaded.Load())/1e6)
	return nil
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func writeZeros(path string, n int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, io.LimitReader(zeros{}, n)); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func du(root string) (int64, error) {
	var n int64
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		info, err := d.Info()
		if err == nil {
			n += info.Size()
		}
		return err
	})
	return n, err
}

// serveSOCKS answers SOCKS5 CONNECTs without authentication: to sinkPort it counts what
// arrives and reports when size bytes did, from sourcePort it sends size zeros.
func serveSOCKS(l net.Listener, expect, received *atomic.Int64, sinkDone chan<- time.Time) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			port, err := handshake(c)
			if err != nil {
				log.Print("socks: ", err)
				return
			}
			switch port {
			case sinkPort:
				n, _ := io.Copy(io.Discard, io.LimitReader(c, expect.Load()))
				received.Store(n)
				sinkDone <- time.Now()
			case sourcePort:
				_, _ = io.Copy(c, io.LimitReader(zeros{}, expect.Load()))
			case 443:
				up, err := net.Dial("tcp", httpsAddr.Load().(string))
				if err != nil {
					log.Print("https: ", err)
					return
				}
				defer up.Close()
				go func() { _, _ = io.Copy(up, c); up.(*net.TCPConn).CloseWrite() }()
				_, _ = io.Copy(c, up)
			}
		}()
	}
}

// httpsAddr is the local HTTPS server the proxy hands port 443 to.
var httpsAddr atomic.Value

// uploadServer counts what is POSTed to it, over TLS with a certificate for 1.1.1.1 from a CA
// of its own, which it returns.
func uploadServer(n *atomic.Int64) (*httptest.Server, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "spike"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("1.1.1.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m, _ := io.Copy(io.Discard, r.Body)
		n.Store(m)
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	return srv, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func handshake(c net.Conn) (int, error) {
	head := make([]byte, 2)
	if _, err := io.ReadFull(c, head); err != nil || head[0] != 5 {
		return 0, fmt.Errorf("greeting: %v", err)
	}
	if _, err := io.ReadFull(c, make([]byte, head[1])); err != nil {
		return 0, err
	}
	if _, err := c.Write([]byte{5, 0}); err != nil {
		return 0, err
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil || req[1] != 1 {
		return 0, fmt.Errorf("request %v: %v", req, err)
	}
	var skip int
	switch req[3] {
	case 1:
		skip = 4
	case 4:
		skip = 16
	case 3:
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return 0, err
		}
		skip = int(l[0])
	default:
		return 0, fmt.Errorf("address type %d", req[3])
	}
	addr := make([]byte, skip+2)
	if _, err := io.ReadFull(c, addr); err != nil {
		return 0, err
	}
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint16(addr[skip:])), nil
}
