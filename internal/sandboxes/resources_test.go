package sandboxes

import (
	"context"
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type resourceRecordingRuntime struct {
	SandboxRuntime
	createCalls int
	specs       []runtime.Spec
}

func (r *resourceRecordingRuntime) Create(ctx context.Context, name string, spec runtime.Spec, socket string, labels map[string]string) error {
	r.createCalls++
	r.specs = append(r.specs, spec)
	return r.SandboxRuntime.Create(ctx, name, spec, socket, labels)
}

type resourceCountingEgress struct{ attachCalls int }

func (e *resourceCountingEgress) Attach(sb store.Sandbox) (runtime.Egress, error) {
	e.attachCalls++
	return fakeEgress{}.Attach(sb)
}

func (*resourceCountingEgress) Detach(string) {}

func TestCreateValidatesResourcesBeforeStoreEgressOrRuntime(t *testing.T) {
	f := newCheckpointFixture(t)
	rt := &resourceRecordingRuntime{SandboxRuntime: f.runtime}
	egress := &resourceCountingEgress{}
	f.manager.Runtime, f.manager.Egress = rt, egress
	before, err := f.store.AllSandboxes(f.ctx)
	if err != nil {
		t.Fatal(err)
	}

	invalid := []CreateRequest{
		{Name: "bad-max-memory", MemoryMiB: 1024, MaxMemoryMiB: 512},
		{Name: "negative-memory", MemoryMiB: -1},
		{Name: "negative-workspace", WorkspaceMiB: -1},
		{Name: "negative-docker", DockerMiB: -1},
	}
	overflow := int64(1 << 32)
	if int64(int(overflow)) == overflow {
		invalid = append(invalid,
			CreateRequest{Name: "overflow-max-memory", MaxMemoryMiB: int(overflow)},
			CreateRequest{Name: "overflow-memory", MemoryMiB: int(overflow)},
			CreateRequest{Name: "overflow-workspace", WorkspaceMiB: int(overflow)},
			CreateRequest{Name: "overflow-docker", DockerMiB: int(overflow)},
		)
	}
	for _, req := range invalid {
		if _, err := f.manager.Create(f.ctx, f.env.ID, req); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("Create(%q) error = %v; want ErrInvalidSpec", req.Name, err)
		}
	}
	after, err := f.store.AllSandboxes(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || egress.attachCalls != 0 || rt.createCalls != 0 {
		t.Fatalf("invalid request had side effects: sandboxes %d -> %d, egress=%d, runtime=%d", len(before), len(after), egress.attachCalls, rt.createCalls)
	}
}

func TestCreatePersistsAndPassesMaxMemory(t *testing.T) {
	f := newCheckpointFixture(t)
	rt := &resourceRecordingRuntime{SandboxRuntime: f.runtime}
	f.manager.Runtime = rt

	explicit, err := f.manager.Create(f.ctx, f.env.ID, CreateRequest{
		Name: "memory-growth", CPUs: 1, MemoryMiB: 1024, MaxMemoryMiB: 2048,
		WorkspaceMiB: 1024, DockerMiB: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.MaxMemoryMiB != 2048 || len(rt.specs) != 1 || rt.specs[0].MaxMemoryMiB != 2048 {
		t.Fatalf("explicit max memory: view=%d runtime spec=%+v", explicit.MaxMemoryMiB, rt.specs)
	}
	stored, err := f.store.Sandbox(f.ctx, f.env.ID, explicit.ID)
	if err != nil || stored.MaxMemoryMiB != 2048 {
		t.Fatalf("stored explicit max memory = %+v, %v", stored, err)
	}

	defaulted, err := f.manager.Create(f.ctx, f.env.ID, CreateRequest{
		Name: "memory-default", CPUs: 1, MemoryMiB: 1536,
		WorkspaceMiB: 1024, DockerMiB: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if defaulted.MaxMemoryMiB != 1536 || len(rt.specs) != 2 || rt.specs[1].MaxMemoryMiB != 1536 {
		t.Fatalf("omitted max memory: view=%d runtime spec=%+v", defaulted.MaxMemoryMiB, rt.specs)
	}
}
