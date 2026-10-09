package runtime

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckpointRemovalRefusesParentBeforeMovingHead(t *testing.T) {
	const group = "sbx-sandbox"
	records := []checkpointRecord{
		{id: "older", group: group},
		{id: "newer", group: group, parentID: "older"},
	}

	if _, err := checkpointRemovalParent(records, group, "older"); !errors.Is(err, ErrCheckpointInUse) {
		t.Fatalf("checkpointRemovalParent error = %v; want ErrCheckpointInUse", err)
	}
}

func TestCheckpointRemovalErrorClassifiesChildAddedAfterPreflight(t *testing.T) {
	const group = "sbx-sandbox"
	removeErr := errors.New("SDK refused removal because an indexed child exists")
	records := []checkpointRecord{
		{id: "older", group: group},
		{id: "newer", group: group, parentID: "older"},
	}

	err := checkpointRemovalError(records, group, "older", removeErr)
	if !errors.Is(err, ErrCheckpointInUse) {
		t.Fatalf("checkpointRemovalError = %v; want ErrCheckpointInUse", err)
	}
	if got := err.Error(); got != ErrCheckpointInUse.Error() {
		t.Fatalf("checkpointRemovalError message = %q; want the plain dependency message", got)
	}
}

func TestCheckpointRemovalHeadPrefersParentID(t *testing.T) {
	const group = "sbx-sandbox"
	records := []checkpointRecord{
		{id: "parent-id", group: group},
		{id: "checkpoint-id", group: group, parentID: "parent-id"},
		{id: "other-sandbox-checkpoint", group: "sbx-other"},
	}

	parentID, err := checkpointRemovalParent(records, group, "checkpoint-id")
	if err != nil {
		t.Fatalf("checkpointRemovalParent: %v", err)
	}
	if got := checkpointRemovalHead(records, group, "checkpoint-id", parentID, "checkpoint-id"); got != "parent-id" {
		t.Fatalf("checkpointRemovalHead = %q; want parent snapshot ID %q", got, "parent-id")
	}
}

func TestCheckpointRemovalHeadFallsBackOnlyWithinOwnedGroup(t *testing.T) {
	const group = "sbx-sandbox"
	records := []checkpointRecord{
		{id: "old-head-id", group: group, parentID: "missing-parent-id"},
		{id: "owned-survivor", group: group},
		{id: "foreign-member", group: "sbx-other"},
	}

	if got := checkpointRemovalHead(records, group, "old-head-id", "missing-parent-id", "old-head-id"); got != "owned-survivor" {
		t.Fatalf("checkpointRemovalHead = %q; want same-group member %q", got, "owned-survivor")
	}
}

func TestCheckpointRemovalHeadLeavesSingletonForSDKToClear(t *testing.T) {
	const group = "sbx-sandbox"
	records := []checkpointRecord{{id: "only-member", group: group}}

	if got := checkpointRemovalHead(records, group, "only-member", "", "only-member"); got != "" {
		t.Fatalf("checkpointRemovalHead = %q; want empty head selection for singleton", got)
	}
}

func TestCheckpointRemovalDoesNotMoveAnUnrelatedGroupHead(t *testing.T) {
	const group = "sbx-sandbox"
	records := []checkpointRecord{
		{id: "target", group: group},
		{id: "same-group-member", group: group},
	}

	if got := checkpointRemovalHead(records, group, "target", "", "same-group-member"); got != "" {
		t.Fatalf("checkpointRemovalHead = %q; want no head change", got)
	}
}

// storedConfig is msb 0.7.7's stored configuration of a sandbox Create made.
const storedConfig = `{"init":null,"network":{
 "policy":{"default_egress":"deny","default_ingress":"allow","rules":[
  {"direction":"egress","destination":{"group":"host"},"protocols":["udp","tcp"],"ports":[{"start":53,"end":53}],"action":"allow"},
  {"direction":"egress","destination":{"group":"public"},"protocols":["tcp"],"ports":[],"action":"allow"}]},
 "dns":{"rebind_protection":true,"nameservers":["127.0.0.1:17102"],"query_timeout_ms":5000},
 "outbound_proxy":{"protocol":"socks5","address":"127.0.0.1:7879",
  "credentials":{"username":"cakcsogfdpwb4","password":{"kind":"env","var":"STUDIO_GW_CAKCSOGFDPWB4"}}}}}`

func TestCheckEgress(t *testing.T) {
	egress := Egress{Nameserver: "127.0.0.1:17102", Proxy: "127.0.0.1:7879", User: "cakcsogfdpwb4", PasswordEnv: "STUDIO_GW_CAKCSOGFDPWB4"}
	if err := checkEgress(storedConfig, egress); err != nil {
		t.Fatalf("Create's configuration refused: %v", err)
	}
	for name, edit := range map[string][2]string{
		"no network":       {`"network":{`, `"network_gone":{`},
		"default allow":    {`"default_egress":"deny"`, `"default_egress":"allow"`},
		"public udp":       {`"protocols":["tcp"],"ports":[]`, `"protocols":["tcp","udp"],"ports":[]`},
		"private tcp":      {`{"group":"public"}`, `{"group":"private"}`},
		"no resolver":      {`"dns":{`, `"dns_gone":{`},
		"other resolver":   {`127.0.0.1:17102`, `127.0.0.1:17103`},
		"no proxy":         {`"outbound_proxy":{`, `"proxy_gone":{`},
		"other proxy":      {`127.0.0.1:7879`, `127.0.0.1:7880`},
		"other identity":   {`"username":"cakcsogfdpwb4"`, `"username":"someoneelse"`},
		"other secret":     {`"var":"STUDIO_GW_CAKCSOGFDPWB4"`, `"var":"STUDIO_GW_OTHER"`},
		"no credentials":   {`"credentials":{"username"`, `"creds":{"username"`},
		"not socks5":       {`"protocol":"socks5"`, `"protocol":"http"`},
		"dns to elsewhere": {`{"group":"host"},"protocols":["udp","tcp"],"ports":[{"start":53,"end":53}]`, `{"group":"public"},"protocols":["udp","tcp"],"ports":[{"start":53,"end":53}]`},
	} {
		if !strings.Contains(storedConfig, edit[0]) {
			t.Fatalf("%s: the fixture has no %q", name, edit[0])
		}
		if err := checkEgress(strings.Replace(storedConfig, edit[0], edit[1], 1), egress); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
