package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExplainPull(t *testing.T) {
	var asked []string
	public := true
	real := anonymousPullable
	t.Cleanup(func() { anonymousPullable = real })
	anonymousPullable = func(_ context.Context, ref string) error {
		asked = append(asked, ref)
		if !public {
			return errors.New("401")
		}
		return nil
	}
	refused := errors.New("image error: registry error: Not authorized: url https://ghcr.io/v2/lukaskoebe/sandbox-studio-base/manifests/sha256:992d")

	err := explainPull(context.Background(), refused)
	var stale *StaleLoginError
	if !errors.As(err, &stale) || stale.Host != "ghcr.io" || !errors.Is(err, refused) ||
		!strings.Contains(err.Error(), "docker logout ghcr.io") {
		t.Fatalf("public image: %v", err)
	}
	if asked[0] != "ghcr.io/lukaskoebe/sandbox-studio-base@sha256:992d" {
		t.Fatalf("checked %q", asked[0])
	}
	tagged := errors.New("Not authorized: url https://registry.example.com/v2/team/app/manifests/v1")
	if explainPull(context.Background(), tagged); asked[1] != "registry.example.com/team/app:v1" {
		t.Fatalf("checked %q", asked[1])
	}

	// A private image needs the login, so the error stays as it was.
	public = false
	if err := explainPull(context.Background(), refused); err != refused {
		t.Fatalf("private image: %v", err)
	}
	other := errors.New("image error: no space left on device")
	if err := explainPull(context.Background(), other); err != other || len(asked) != 3 {
		t.Fatalf("other error: %v, %d checks", err, len(asked))
	}
}
