//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const (
	networkApprovalDelay = 500 * time.Millisecond
	// The public gateway dialer can take 15 seconds before recording a failed
	// connection; leave 5 seconds for the private probe to observe that result.
	networkConnectionWait      = 20 * time.Second
	networkPolicyHold          = 60 * time.Second
	networkGatewayDrainTimeout = 80 * time.Second
	networkGatewayPollInterval = 25 * time.Millisecond
)

// networkInstallApprovals is deliberately host-and-port exact. Redirects or new
// package/tool hosts must be reviewed before they are added here.
var networkInstallApprovals = map[string]map[int]struct{}{
	"deb.debian.org": {
		80: struct{}{}, 443: struct{}{},
	},
	"security.debian.org": {
		80: struct{}{}, 443: struct{}{},
	},
	"download.docker.com": {
		443: struct{}{},
	},
	"nodejs.org": {
		443: struct{}{},
	},
	"mise-versions.jdx.dev": {
		443: struct{}{},
	},
	"mise.jdx.dev": {
		443: struct{}{},
	},
}

type networkInstallTarget struct {
	Host string
	Port int
}

type networkConnectionEvidence struct {
	SandboxID string
	Conn      gateway.Conn
}

func networkInstallHostAllowed(host string, port int) bool {
	ports, ok := networkInstallApprovals[host]
	if !ok {
		return false
	}
	_, ok = ports[port]
	return ok
}

// validatePrivateNetworkApproval is intentionally independent of Store access so its
// environment, durable-job, sandbox-owner, and host checks can be tested in isolation.
func validatePrivateNetworkApproval(approval store.Approval, envID string, job store.BuildJob, sandbox store.Sandbox) (networkInstallTarget, error) {
	var request policy.NetworkRequest
	if err := json.Unmarshal(approval.Payload, &request); err != nil {
		return networkInstallTarget{}, errors.New("network approval payload is invalid")
	}
	target := networkInstallTarget{Host: policy.Normalize(request.Host), Port: request.Port}
	if target.Host == "" || target.Host != request.Host || target.Port < 1 || target.Port > 65535 {
		return target, errors.New("network approval host or port is invalid")
	}
	if approval.EnvironmentID != envID || approval.SandboxID == "" || approval.Kind != policy.KindNetwork ||
		approval.Status != store.StatusPending || approval.Subject != target.Host+":"+strconv.Itoa(target.Port) ||
		job.EnvironmentID != envID || job.ID == "" || job.SandboxID == "" || job.SandboxID != sandbox.ID ||
		!activeNetworkBuild(job.Status) || sandbox.EnvironmentID != envID || sandbox.BuildJobID != job.ID ||
		approval.SandboxID != job.SandboxID {
		return target, errors.New("network approval does not belong to the active private build sandbox")
	}
	if !networkInstallHostAllowed(target.Host, target.Port) {
		return target, fmt.Errorf("network approval host %q is outside the exact network-install allowlist", target.Host)
	}
	return target, nil
}

func activeNetworkBuild(status string) bool {
	return status == store.BuildPreparing || status == store.BuildSettingUp || status == store.BuildExporting
}

func freeLoopbackTCPPort() (int, error) {
	for attempt := 0; attempt < 8; attempt++ {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return 0, err
		}
		_, portText, splitErr := net.SplitHostPort(listener.Addr().String())
		closeErr := listener.Close()
		if splitErr != nil {
			return 0, splitErr
		}
		if closeErr != nil {
			return 0, closeErr
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			return 0, err
		}
		if port >= 20000 && port != 17100 {
			return port, nil
		}
	}
	return 0, errors.New("could not select a high ephemeral loopback port for the private resolver")
}

func retryableResolverPortError(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

func pendingConnection(connections []gateway.Conn, target networkInstallTarget) (gateway.Conn, bool) {
	for _, connection := range connections {
		if connection.Host == target.Host && connection.Port == target.Port && connection.Verdict == gateway.VerdictPending {
			return connection, true
		}
	}
	return gateway.Conn{}, false
}

func openedConnection(connections []gateway.Conn, id uint64, target networkInstallTarget, ruleID string) (gateway.Conn, bool) {
	for _, connection := range connections {
		if connection.ID == id && connection.Host == target.Host && connection.Port == target.Port && connection.RuleID == ruleID &&
			(connection.Verdict == gateway.VerdictOpen || connection.Verdict == gateway.VerdictAllowed) {
			return connection, true
		}
	}
	return gateway.Conn{}, false
}

// trackedGatewayListener lets teardown close the private listener and then wait for
// accepted gateway handlers before the private Store is closed.
type trackedGatewayListener struct {
	net.Listener
	mu     sync.Mutex
	active int
}

func (l *trackedGatewayListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.active++
	l.mu.Unlock()
	return &trackedGatewayConn{Conn: conn, release: func() {
		l.mu.Lock()
		l.active--
		l.mu.Unlock()
	}}, nil
}

func (l *trackedGatewayListener) wait(ctx context.Context) error {
	ticker := time.NewTicker(networkGatewayPollInterval)
	defer ticker.Stop()
	for {
		l.mu.Lock()
		active := l.active
		l.mu.Unlock()
		if active == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type trackedGatewayConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *trackedGatewayConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
