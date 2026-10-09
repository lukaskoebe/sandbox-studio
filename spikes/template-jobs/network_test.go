//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

func TestNetworkInstallHostAllowlistIsExact(t *testing.T) {
	allowed := []networkInstallTarget{
		{Host: "deb.debian.org", Port: 80},
		{Host: "deb.debian.org", Port: 443},
		{Host: "security.debian.org", Port: 80},
		{Host: "security.debian.org", Port: 443},
		{Host: "download.docker.com", Port: 443},
		{Host: "nodejs.org", Port: 443},
		{Host: "mise-versions.jdx.dev", Port: 443},
		{Host: "mise.jdx.dev", Port: 443},
	}
	for _, target := range allowed {
		if !networkInstallHostAllowed(target.Host, target.Port) {
			t.Errorf("networkInstallHostAllowed(%q, %d) = false; want true", target.Host, target.Port)
		}
	}
	rejected := []networkInstallTarget{
		{Host: "*.debian.org", Port: 443},
		{Host: "mirror.deb.debian.org", Port: 443},
		{Host: "download.docker.com", Port: 80},
		{Host: "nodejs.org", Port: 80},
		{Host: "mise-versions.jdx.dev", Port: 80},
		{Host: "mise.jdx.dev", Port: 80},
		{Host: "NODEJS.org", Port: 443},
	}
	for _, target := range rejected {
		if networkInstallHostAllowed(target.Host, target.Port) {
			t.Errorf("networkInstallHostAllowed(%q, %d) = true; want false", target.Host, target.Port)
		}
	}
}

func TestValidatePrivateNetworkApprovalRequiresExactActiveOwner(t *testing.T) {
	const envID = "private-env"
	job := store.BuildJob{ID: "job-1", EnvironmentID: envID, SandboxID: "builder-1", Status: store.BuildSettingUp}
	sandbox := store.Sandbox{ID: "builder-1", EnvironmentID: envID, BuildJobID: job.ID}
	approval := networkApprovalForTest(envID, sandbox.ID, "deb.debian.org", 443)
	if target, err := validatePrivateNetworkApproval(approval, envID, job, sandbox); err != nil || target != (networkInstallTarget{Host: "deb.debian.org", Port: 443}) {
		t.Fatalf("validatePrivateNetworkApproval() = %+v, %v; want exact builder target", target, err)
	}

	cases := []struct {
		name     string
		approval store.Approval
		job      store.BuildJob
		sandbox  store.Sandbox
	}{
		{name: "approval from another environment", approval: func() store.Approval { a := approval; a.EnvironmentID = "other-env"; return a }(), job: job, sandbox: sandbox},
		{name: "approval from another sandbox", approval: func() store.Approval { a := approval; a.SandboxID = "other-sandbox"; return a }(), job: job, sandbox: sandbox},
		{name: "job no longer owns sandbox", approval: approval, job: func() store.BuildJob { j := job; j.SandboxID = "other-sandbox"; return j }(), sandbox: sandbox},
		{name: "sandbox owner differs", approval: approval, job: job, sandbox: func() store.Sandbox { sb := sandbox; sb.BuildJobID = "other-job"; return sb }()},
		{name: "terminal job", approval: approval, job: func() store.BuildJob { j := job; j.Status = store.BuildReady; return j }(), sandbox: sandbox},
		{name: "not pending", approval: func() store.Approval { a := approval; a.Status = store.StatusApproved; return a }(), job: job, sandbox: sandbox},
		{name: "subject does not match payload", approval: func() store.Approval { a := approval; a.Subject = "other.example:443"; return a }(), job: job, sandbox: sandbox},
		{name: "wrong approval kind", approval: func() store.Approval { a := approval; a.Kind = "other"; return a }(), job: job, sandbox: sandbox},
		{name: "wrong port", approval: networkApprovalForTest(envID, sandbox.ID, "deb.debian.org", 22), job: job, sandbox: sandbox},
		{name: "unknown host", approval: networkApprovalForTest(envID, sandbox.ID, "redirect.example", 443), job: job, sandbox: sandbox},
		{name: "wildcard host", approval: networkApprovalForTest(envID, sandbox.ID, "*.debian.org", 443), job: job, sandbox: sandbox},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validatePrivateNetworkApproval(tc.approval, envID, tc.job, tc.sandbox); err == nil {
				t.Fatal("validatePrivateNetworkApproval accepted a foreign, inactive, or non-exact approval")
			}
		})
	}
}

func TestNetworkBuildSourcePinsAptAndMiseToolAndKeepsCanonicalVariants(t *testing.T) {
	setup := networkSetupScript("NETWORK_BUILD_TEST_MARKER")
	blockSpec, err := templatespec.ParseYAML([]byte(networkBuildSource(setup, false)))
	if err != nil {
		t.Fatalf("parse network-install block source: %v", err)
	}
	reorderedSpec, err := templatespec.ParseYAML([]byte(networkBuildSource(setup, true)))
	if err != nil {
		t.Fatalf("parse reordered network-install source: %v", err)
	}
	blockCanonical, err := templatespec.CanonicalJSON(blockSpec)
	if err != nil {
		t.Fatalf("canonicalize network-install block source: %v", err)
	}
	reorderedCanonical, err := templatespec.CanonicalJSON(reorderedSpec)
	if err != nil {
		t.Fatalf("canonicalize reordered network-install source: %v", err)
	}
	if !bytes.Equal(blockCanonical, reorderedCanonical) {
		t.Fatalf("network source variants differ after canonicalization:\nblock:     %s\nreordered: %s", blockCanonical, reorderedCanonical)
	}
	if len(blockSpec.Apt) != 1 || blockSpec.Apt[0] != "tree" || len(blockSpec.Tools) != 1 ||
		blockSpec.Tools[0] != (templatespec.ToolPin{Name: "node", Version: "22.14.0"}) {
		t.Fatalf("network-install spec pins = apt:%v tools:%v; want tree and node 22.14.0", blockSpec.Apt, blockSpec.Tools)
	}
}

func networkApprovalForTest(envID, sandboxID, host string, port int) store.Approval {
	payload, _ := json.Marshal(policy.NetworkRequest{Host: host, Port: port})
	return store.Approval{
		ID: "approval-1", EnvironmentID: envID, SandboxID: sandboxID, Kind: policy.KindNetwork,
		Subject: host + ":" + strconv.Itoa(port), Payload: payload, Status: store.StatusPending,
	}
}
