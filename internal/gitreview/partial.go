package gitreview

import (
	"context"
	"errors"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
)

// Partial mirrors. A staging repository mirrors the forge to check and describe a push.
// That takes the forge's commits and folders, but of its files only the ones a push
// changes. A forge that filters a fetch and hands out objects by ID (GitHub, GitLab,
// Forgejo, a git server set up for partial clones) is therefore mirrored without file
// contents, and a review fetches the contents it shows. A repository with gigabytes of
// files in its history is mirrored in a megabyte this way, where the whole of it wouldn't
// arrive within mirrorFetchTimeout. Other forges are mirrored whole, and so are the ones
// whose remote says so.

const (
	// blobFetchTimeout bounds fetching the file contents of one diff.
	blobFetchTimeout = time.Minute
	// maxFetchBlobs is how many files' contents one diff fetches.
	maxFetchBlobs = 2000
	// packOverhead is more than a pack of one blob takes on top of the blob.
	packOverhead = 4 << 10
)

// partial reports whether a forge advertising caps is mirrored without file contents.
func (rm *remote) partial(caps *capability.List) bool {
	return !rm.whole && caps.Supports(capability.Filter) && caps.Supports(capability.AllowReachableSHA1InWant)
}

// errNoObjectFetch is a forge that doesn't hand out objects by ID.
var errNoObjectFetch = errors.New("the forge does not hand out objects by ID")

// fetchError is a diff that failed because the forge didn't hand out the changed files.
type fetchError struct{ err error }

func (e *fetchError) Error() string {
	return "fetch the changed files from the forge: " + e.err.Error()
}
func (e *fetchError) Unwrap() error { return e.err }

// fetchObjects fetches the objects want by ID into the staging repository: a pack of at
// most limit bytes, or the fetch fails as too large and stores nothing.
func (s *Service) fetchObjects(ctx context.Context, rm *remote, r *git.Repository, want []plumbing.Hash, limit int64) error {
	sess, err := rm.tr.NewUploadPackSession(rm.ep, rm.auth)
	if err != nil {
		return err
	}
	defer sess.Close()
	ar, err := sess.AdvertisedReferencesContext(ctx)
	if err != nil {
		return err
	}
	if !rm.partial(ar.Capabilities) {
		return errNoObjectFetch
	}
	req := packp.NewUploadPackRequestFromCapabilities(ar.Capabilities)
	req.Wants = want
	if ar.Capabilities.Supports(capability.NoProgress) {
		_ = req.Capabilities.Set(capability.NoProgress) // it takes no arguments, so it can't fail
	}
	resp, err := sess.UploadPack(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Close()
	// Through a scratch file, so a fetch cut short leaves nothing in the repository.
	return s.ingestPack(r, &limitedBody{r: packOf(req, resp), n: limit})
}

// lacking returns the objects of hs the repository doesn't have, each once.
func lacking(r *git.Repository, hs []plumbing.Hash) []plumbing.Hash {
	var out []plumbing.Hash
	seen := make(map[plumbing.Hash]bool, len(hs))
	for _, h := range hs {
		if !seen[h] && r.Storer.HasEncodedObject(h) != nil {
			out = append(out, h)
		}
		seen[h] = true
	}
	return out
}

// fetchBlobs fetches the blobs of want that the staging repository lacks from a forge
// mirrored without them. Blobs that together are larger than a push may be are fetched one
// by one, and those larger than a diff shows are left out. They stay missing, as do the
// blobs past maxFetchBlobs and the ones that take longer than blobFetchTimeout one by one.
func (s *Service) fetchBlobs(ctx context.Context, rm *remote, r *git.Repository, want []plumbing.Hash) error {
	if rm == nil || rm.whole {
		return nil
	}
	missing := lacking(r, want)
	if len(missing) == 0 {
		return nil
	}
	if len(missing) > maxFetchBlobs {
		missing = missing[:maxFetchBlobs]
	}
	ctx, cancel := context.WithTimeout(ctx, blobFetchTimeout)
	defer cancel()
	err := s.fetchObjects(ctx, rm, r, missing, s.Limits.MaxPack)
	switch {
	case errors.Is(err, errNoObjectFetch):
		return nil // a whole mirror: what it lacks, the forge lacks
	case !tooLarge(err):
		return err
	}
	for _, h := range missing {
		err := s.fetchObjects(ctx, rm, r, []plumbing.Hash{h}, s.Limits.MaxBlob+packOverhead)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && !tooLarge(err) {
			return err
		}
	}
	return nil
}

// changedBlobs lists the blobs on either side of the changes from one tree to another.
// It reads folders only.
func changedBlobs(ctx context.Context, from, to *object.Tree) ([]plumbing.Hash, error) {
	changes, err := object.DiffTreeWithOptions(ctx, from, to, &object.DiffTreeOptions{})
	if err != nil {
		return nil, err
	}
	var out []plumbing.Hash
	for _, ch := range changes {
		for _, e := range []object.ChangeEntry{ch.From, ch.To} {
			if e.Name != "" && e.TreeEntry.Mode.IsFile() {
				out = append(out, e.TreeEntry.Hash)
			}
		}
	}
	return out, nil
}

// fetchChanged fetches the contents of the files that differ between two trees, for a
// diff of them.
func (s *Service) fetchChanged(ctx context.Context, rm *remote, r *git.Repository, from, to *object.Tree) error {
	if rm == nil || rm.whole {
		return nil
	}
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	blobs, err := changedBlobs(lctx, from, to)
	cancel()
	if err != nil {
		return err
	}
	if err := s.fetchBlobs(ctx, rm, r, blobs); err != nil {
		return &fetchError{err}
	}
	return nil
}

// diffChanges lists the changes from one tree to another, with renames. A rename that
// also edits the file shows in the files' contents; where the repository lacks some, only
// renames of unchanged files are found.
func diffChanges(ctx context.Context, r *git.Repository, from, to *object.Tree) (object.Changes, error) {
	blobs, err := changedBlobs(ctx, from, to)
	if err != nil {
		return nil, err
	}
	opts := *object.DefaultDiffTreeOptions
	opts.OnlyExactRenames = len(lacking(r, blobs)) > 0
	return object.DiffTreeWithOptions(ctx, from, to, &opts)
}
