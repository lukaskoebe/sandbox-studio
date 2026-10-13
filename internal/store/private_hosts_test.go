package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestPrivateHostsArePerEnvironment(t *testing.T) {
	ctx := context.Background()
	s, work, other := openTeammateStore(t)

	if hosts, err := s.PrivateHosts(ctx, work.ID); err != nil || len(hosts) != 0 {
		t.Fatalf("new environment: %v, %+v", err, hosts)
	}
	list := []PrivateHost{{Host: "git.company.local"}, {Host: "*.corp.local", Ports: []int{22, 8443}}}
	if err := s.SetPrivateHosts(ctx, work.ID, list); err != nil {
		t.Fatal(err)
	}
	got, err := s.PrivateHosts(ctx, work.ID)
	if err != nil || !reflect.DeepEqual(got, list) {
		t.Fatalf("read back: %v, %+v", err, got)
	}
	if hosts, _ := s.PrivateHosts(ctx, other.ID); len(hosts) != 0 {
		t.Fatalf("other environment sees %+v", hosts)
	}

	if err := s.SetPrivateHosts(ctx, work.ID, nil); err != nil {
		t.Fatal(err)
	}
	if hosts, err := s.PrivateHosts(ctx, work.ID); err != nil || len(hosts) != 0 {
		t.Fatalf("cleared: %v, %+v", err, hosts)
	}
	if _, err := s.PrivateHosts(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown environment: %v", err)
	}
	if err := s.SetPrivateHosts(ctx, "missing", list); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set on unknown environment: %v", err)
	}
}
