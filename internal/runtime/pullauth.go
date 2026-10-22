package runtime

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
)

// StaleLoginError is a pull that the registry refused with the login saved on this computer,
// of an image that it serves to anyone. microsandbox takes the first credentials it finds
// (its own, then Docker's config and credential helpers) and doesn't fall back to an
// anonymous pull, so an expired `docker login` breaks pulls of public images.
type StaleLoginError struct {
	Host string
	Err  error
}

func (e *StaleLoginError) Error() string {
	return fmt.Sprintf("%s refused the login saved on this computer (it's probably expired). The image is public: run `docker logout %s` (or `msb registry logout %s`), then try again", e.Host, e.Host, e.Host)
}

func (e *StaleLoginError) Unwrap() error { return e.Err }

// refusedPull matches microsandbox's error for a manifest the registry refused, such as
// "registry error: Not authorized: url https://ghcr.io/v2/owner/image/manifests/sha256:…".
var refusedPull = regexp.MustCompile(`Not authorized: url https?://([^/\s]+)/v2/(\S+)/manifests/(\S+)`)

// anonymousPullable checks without credentials that the registry serves ref.
var anonymousPullable = (&doctor.Registry{Client: &http.Client{Timeout: 10 * time.Second}}).Pullable

// explainPull turns a refused pull into a StaleLoginError when the image can be pulled
// without a login, and returns any other error as it is.
func explainPull(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	m := refusedPull.FindStringSubmatch(err.Error())
	if m == nil {
		return err
	}
	ref := m[1] + "/" + m[2]
	if strings.HasPrefix(m[3], "sha256:") {
		ref += "@" + m[3]
	} else {
		ref += ":" + m[3]
	}
	if anonymousPullable(context.WithoutCancel(ctx), ref) != nil {
		return err
	}
	return &StaleLoginError{Host: m[1], Err: err}
}
