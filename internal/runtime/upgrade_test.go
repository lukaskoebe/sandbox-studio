package runtime

import (
	"context"
	"errors"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func TestOlder(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.7.7", "0.7.8", true},
		{"0.7.8", "0.7.8", false},
		{"0.7.9", "0.7.8", false},
		{"0.6.12", "0.7.0", true},
		{"0.7.10", "0.7.9", false},
		{"v0.7.7", "0.7.8", true},
		{"0.7.8-rc.1", "0.7.8", true},
		{"0.7.8", "0.7.8-rc.1", false},
		{"nightly", "0.7.8", false},
		{"0.7", "0.7.8", false},
	} {
		if got := older(c.a, c.b); got != c.want {
			t.Errorf("older(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// fakeInstaller has msb resolve at origin with version have, and records installs.
func fakeInstaller(origin msb.RuntimeOrigin, have string, installed *[]string) installer {
	return installer{
		ensure: func(context.Context) (msb.ResolvedRuntime, error) {
			return msb.ResolvedRuntime{MSBPath: "/home/u/.microsandbox/bin/msb", Origin: origin}, nil
		},
		version: func(context.Context, string) (string, error) { return have, nil },
		install: func(_ context.Context, v string) error {
			*installed = append(*installed, v)
			return nil
		},
		sdk: "v0.7.8",
	}
}

func TestEnsureUpgradesAnOlderMSB(t *testing.T) {
	var installed []string
	up, err := fakeInstaller(msb.RuntimeOriginHome, "0.7.7", &installed).run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if up == nil || up.From != "0.7.7" || up.To != "0.7.8" || len(installed) != 1 || installed[0] != "0.7.8" {
		t.Fatalf("upgrade = %+v, installed %v", up, installed)
	}
}

func TestEnsureLeavesOtherMSBsAlone(t *testing.T) {
	for _, c := range []struct {
		name   string
		origin msb.RuntimeOrigin
		have   string
	}{
		{"same version", msb.RuntimeOriginHome, "0.7.8"},
		{"newer", msb.RuntimeOriginInstalled, "0.7.9"},
		{"from MSB_PATH", msb.RuntimeOriginEnvironment, "0.7.7"},
		{"configured", msb.RuntimeOriginConfiguration, "0.7.7"},
		{"unparsable", msb.RuntimeOriginHome, "dev"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var installed []string
			up, err := fakeInstaller(c.origin, c.have, &installed).run(context.Background())
			if err != nil || up != nil || len(installed) != 0 {
				t.Fatalf("upgrade = %+v, %v, installed %v", up, err, installed)
			}
		})
	}
}

func TestEnsureReportsAFailedUpgrade(t *testing.T) {
	in := fakeInstaller(msb.RuntimeOriginHome, "0.7.7", new([]string))
	in.install = func(context.Context, string) error { return errors.New("no network") }
	var upErr *UpgradeError
	if _, err := in.run(context.Background()); !errors.As(err, &upErr) || upErr.From != "0.7.7" {
		t.Fatalf("a failed install: %v", err)
	}
}
