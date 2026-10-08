// Spike S1: route all microsandbox egress (incl. nested Docker) through a host SOCKS5
// gateway that identifies the sandbox by username and decides per connection.
//
// Run: go run ./spikes/s1-gateway   (needs KVM on Linux, Apple Silicon on macOS, WHP on Windows)
package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

var blocked = map[string]bool{"www.wikipedia.org": true}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	if _, err := msb.EnsureRuntime(ctx, msb.RuntimeConfig{}, msb.InstallOptions{}); err != nil {
		log.Fatalf("EnsureRuntime: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	go serve(ln)
	os.Setenv("SPIKE_SOCKS_PW", "s3cret")
	name := "spike-gw"
	_ = msb.RemoveSandbox(ctx, name)
	log.Printf("gateway on %s; creating sandbox", ln.Addr())
	sb, err := msb.CreateSandbox(ctx, name,
		msb.WithImage("docker:dind"),
		msb.WithMemory(2048), msb.WithCPUs(2),
		msb.WithMounts(map[string]msb.MountConfig{
			"/var/lib/docker": msb.Mount.Owned(msb.OwnedVolumeOptions{Kind: msb.VolumeKindDisk, SizeMiB: 8192}),
		}),
		msb.WithNetwork(func() *msb.NetworkConfig { n := msb.NetworkPolicy.FromProfiles(msb.NetworkProfilePublic); n.DNS = &msb.DNSConfig{Nameservers: []string{"1.1.1.1:53", "9.9.9.9:53"}}; return n }()),
		msb.WithProxy(msb.SOCKS5Proxy(ln.Addr().String()).Credentials("sbx-1234", msb.SecretSourceEnv("SPIKE_SOCKS_PW"))),
		msb.WithReplace(),
	)
	if err != nil {
		log.Fatalf("CreateSandbox: %v", err)
	}
	defer func() {
		c, cc := context.WithTimeout(context.Background(), 60*time.Second)
		defer cc()
		sb.Stop(c)
		sb.Close()
		msb.RemoveSandbox(c, name)
	}()
	run := func(cmd string) {
		log.Printf("$ %s", cmd)
		out, err := sb.Shell(ctx, cmd)
		if err != nil {
			log.Printf("  err: %v", err)
			return
		}
		fmt.Printf("  exit=%d\n  stdout: %s\n  stderr: %s\n", out.ExitCode(), trim(out.Stdout()), trim(out.Stderr()))
	}
	run("wget -qO- https://example.com | head -c 120; echo")
	run("wget -qO- http://example.com | head -c 120; echo")
	run("wget -T 10 -qO- https://www.wikipedia.org | head -c 80; echo")
	run("(dockerd >/tmp/dockerd.log 2>&1 &) ; timeout 60 sh -c 'until docker info >/dev/null 2>&1; do sleep 1; done' && docker version --format '{{.Server.Version}}' || tail -20 /tmp/dockerd.log")
	run("docker run --rm alpine:3.20 sh -c 'wget -qO- https://example.com | head -c 80; echo; wget -T 10 -qO- https://www.wikipedia.org | head -c 40'")
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		s = s[:600] + "…"
	}
	return s
}

func serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go handle(c)
	}
}

func handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return
	}
	methods := make([]byte, hdr[1])
	io.ReadFull(br, methods)
	c.Write([]byte{5, 2}) // username/password
	// RFC 1929
	v := make([]byte, 2)
	io.ReadFull(br, v)
	user := make([]byte, v[1])
	io.ReadFull(br, user)
	pl := make([]byte, 1)
	io.ReadFull(br, pl)
	pass := make([]byte, pl[0])
	io.ReadFull(br, pass)
	if string(pass) != "s3cret" {
		c.Write([]byte{1, 1})
		return
	}
	c.Write([]byte{1, 0})
	req := make([]byte, 4)
	io.ReadFull(br, req)
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		io.ReadFull(br, ip)
		host = net.IP(ip).String()
	case 4:
		ip := make([]byte, 16)
		io.ReadFull(br, ip)
		host = net.IP(ip).String()
	case 3:
		l := make([]byte, 1)
		io.ReadFull(br, l)
		d := make([]byte, l[0])
		io.ReadFull(br, d)
		host = string(d)
	}
	pb := make([]byte, 2)
	io.ReadFull(br, pb)
	port := binary.BigEndian.Uint16(pb)
	// Reply success first so the guest sends its first bytes; we decide after peeking.
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	first, _ := br.Peek(1)
	name := ""
	if len(first) == 1 && first[0] == 0x16 {
		name = sniff(br)
	} else if len(first) == 1 {
		name = httpHost(br)
	}
	c.SetReadDeadline(time.Time{})
	verdict := "allow"
	if blocked[name] {
		verdict = "DENY"
	}
	log.Printf("[gw] sandbox=%s cmd=%d dst=%s:%d name=%q -> %s", user, req[1], host, port, name, verdict)
	if verdict != "allow" {
		return
	}
	up, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), 10*time.Second)
	if err != nil {
		return
	}
	defer up.Close()
	go io.Copy(up, br)
	io.Copy(c, up)
}

func httpHost(br *bufio.Reader) string {
	b, _ := br.Peek(br.Buffered())
	for _, line := range strings.Split(string(b), "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "host:") {
			return strings.TrimSpace(line[5:])
		}
	}
	return ""
}

// sniff extracts SNI from a buffered TLS ClientHello without consuming it.
func sniff(br *bufio.Reader) string {
	h, err := br.Peek(5)
	if err != nil {
		return ""
	}
	n := int(binary.BigEndian.Uint16(h[3:5]))
	rec, err := br.Peek(5 + n)
	if err != nil {
		return ""
	}
	s, _ := parseSNI(rec[5:])
	return s
}

func parseSNI(b []byte) (string, error) {
	bad := errors.New("bad hello")
	if len(b) < 4 || b[0] != 1 {
		return "", bad
	}
	p := 4 + 2 + 32
	if p >= len(b) {
		return "", bad
	}
	p += 1 + int(b[p])
	if p+2 > len(b) {
		return "", bad
	}
	p += 2 + int(binary.BigEndian.Uint16(b[p:]))
	if p >= len(b) {
		return "", bad
	}
	p += 1 + int(b[p])
	if p+2 > len(b) {
		return "", bad
	}
	end := p + 2 + int(binary.BigEndian.Uint16(b[p:]))
	p += 2
	for p+4 <= end && end <= len(b) {
		typ := binary.BigEndian.Uint16(b[p:])
		l := int(binary.BigEndian.Uint16(b[p+2:]))
		p += 4
		if typ == 0 && p+5 <= len(b) {
			nl := int(binary.BigEndian.Uint16(b[p+3:]))
			if p+5+nl <= len(b) {
				return string(b[p+5 : p+5+nl]), nil
			}
		}
		p += l
	}
	return "", bad
}
