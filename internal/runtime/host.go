package runtime

import (
	"context"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// MSBPath returns the installed msb executable without installing anything.
func MSBPath() (string, error) {
	r, err := msb.ResolveRuntime(msb.RuntimeConfig{})
	return r.MSBPath, err
}

// MSBVersion is the msb release the SDK was built against.
func MSBVersion() string { return msb.SDKVersion() }

// ImageCached reports whether msb has ref in its image cache.
func ImageCached(ctx context.Context, ref string) (bool, error) {
	_, err := msb.Image.Get(ctx, ref)
	if err != nil {
		if msb.IsKind(err, msb.ErrImageNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
