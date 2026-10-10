package gitreview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/sideband"
	"github.com/go-git/go-git/v5/plumbing/revlist"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A staging repository is a bare repository on the host. refs/heads/* and refs/tags/*
// mirror the forge as of the last push or approval; refs/studio/push/<id> keep each staged
// push. Only Studio writes them, and only objects the sandbox sent or the forge has.

func openStaging(path string) (*git.Repository, error) {
	r, err := git.PlainOpen(path)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, err
		}
		return git.PlainInit(path, true)
	}
	return r, err
}

// remote is a host-side connection to a forge repository with the token.
type remote struct {
	ep   *transport.Endpoint
	auth *githttp.BasicAuth
	tr   transport.Transport
}

func (s *Service) remote(t target, token string) (*remote, error) {
	ad, err := adapter(t.forge, s.httpClient())
	if err != nil {
		return nil, err
	}
	ep, err := transport.NewEndpoint(t.upstreamURL(""))
	if err != nil {
		return nil, err
	}
	user, pass := ad.GitAuth(token)
	return &remote{ep: ep, auth: &githttp.BasicAuth{Username: user, Password: pass}, tr: githttp.NewClient(s.httpClient())}, nil
}

func mirrored(name plumbing.ReferenceName) bool {
	return name.IsBranch() || name.IsTag()
}

// fetchMirror brings the staging repository's branches and tags up to date with the forge.
func (rm *remote) fetchMirror(ctx context.Context, r *git.Repository) error {
	sess, err := rm.tr.NewUploadPackSession(rm.ep, rm.auth)
	if err != nil {
		return err
	}
	defer sess.Close()
	upstream := map[plumbing.ReferenceName]plumbing.Hash{}
	ar, err := sess.AdvertisedReferencesContext(ctx)
	switch {
	case errors.Is(err, transport.ErrEmptyRemoteRepository):
	case err != nil:
		return err
	default:
		for name, h := range ar.References {
			if n := plumbing.ReferenceName(name); mirrored(n) && n.Validate() == nil {
				upstream[n] = h
			}
		}
	}
	local, err := mirrorRefs(r)
	if err != nil {
		return err
	}
	var wants, haves []plumbing.Hash
	seen := map[plumbing.Hash]bool{}
	for _, h := range upstream {
		if !seen[h] && r.Storer.HasEncodedObject(h) != nil {
			wants = append(wants, h)
		}
		seen[h] = true
	}
	for _, h := range local {
		if r.Storer.HasEncodedObject(h) == nil {
			haves = append(haves, h)
		}
	}
	if len(wants) > 0 {
		req := packp.NewUploadPackRequestFromCapabilities(ar.Capabilities)
		req.Wants, req.Haves = wants, haves
		if ar.Capabilities.Supports(capability.NoProgress) {
			req.Capabilities.Set(capability.NoProgress)
		}
		resp, err := sess.UploadPack(ctx, req)
		if err != nil {
			return err
		}
		var pack io.Reader = resp
		switch {
		case req.Capabilities.Supports(capability.Sideband64k):
			pack = sideband.NewDemuxer(sideband.Sideband64k, resp)
		case req.Capabilities.Supports(capability.Sideband):
			pack = sideband.NewDemuxer(sideband.Sideband, resp)
		}
		err = packfile.UpdateObjectStorage(r.Storer, pack)
		resp.Close()
		if err != nil {
			return err
		}
	}
	for name, h := range upstream {
		if err := r.Storer.SetReference(plumbing.NewHashReference(name, h)); err != nil {
			return err
		}
	}
	for name := range local {
		if _, ok := upstream[name]; !ok {
			if err := r.Storer.RemoveReference(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// mirrorRefs returns the staging repository's copy of the forge's branches and tags.
func mirrorRefs(r *git.Repository) (map[plumbing.ReferenceName]plumbing.Hash, error) {
	iter, err := r.Storer.IterReferences()
	if err != nil {
		return nil, err
	}
	out := map[plumbing.ReferenceName]plumbing.Hash{}
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		if ref.Type() == plumbing.HashReference && mirrored(ref.Name()) {
			out[ref.Name()] = ref.Hash()
		}
		return nil
	})
	return out, err
}

func hashes(m map[plumbing.ReferenceName]plumbing.Hash) []plumbing.Hash {
	out := make([]plumbing.Hash, 0, len(m))
	for _, h := range m {
		out = append(out, h)
	}
	return out
}

// pushUpstream updates p's branch on the forge from p.OldSHA to p.NewSHA. It refuses if
// the branch moved on the forge since the sandbox pushed.
func (rm *remote) pushUpstream(ctx context.Context, r *git.Repository, p store.GitPush) (string, error) {
	sess, err := rm.tr.NewReceivePackSession(rm.ep, rm.auth)
	if err != nil {
		return "", err
	}
	defer sess.Close()
	ar, err := sess.AdvertisedReferencesContext(ctx)
	if err != nil {
		return "", err
	}
	name := plumbing.ReferenceName(p.Ref)
	current := ar.References[p.Ref]
	newHash := plumbing.NewHash(p.NewSHA)
	if current == newHash {
		return "the forge already has this commit on " + name.Short(), nil
	}
	if current != plumbing.NewHash(p.OldSHA) {
		return "", fmt.Errorf("%s moved on the forge since the sandbox pushed (now %s); the agent has to fetch, rebase and push again",
			name.Short(), short(current.String()))
	}
	var ignore []plumbing.Hash
	for _, h := range ar.References {
		if r.Storer.HasEncodedObject(h) == nil {
			ignore = append(ignore, h)
		}
	}
	objs, err := revlist.Objects(r.Storer, []plumbing.Hash{newHash}, ignore)
	if err != nil {
		return "", fmt.Errorf("staged objects: %w", err)
	}
	req := packp.NewReferenceUpdateRequestFromCapabilities(ar.Capabilities)
	req.Commands = []*packp.Command{{Name: name, Old: current, New: newHash}}
	pr, pw := io.Pipe()
	req.Packfile = pr
	done := make(chan error, 1)
	go func() {
		_, err := packfile.NewEncoder(pw, r.Storer, false).Encode(objs, 10)
		pw.CloseWithError(err)
		done <- err
	}()
	rs, err := sess.ReceivePack(ctx, req)
	pr.Close()
	if encErr := <-done; err == nil && encErr != nil && !errors.Is(encErr, io.ErrClosedPipe) {
		err = encErr
	}
	if err != nil {
		return "", err
	}
	if rs != nil {
		if err := rs.Error(); err != nil {
			return "", err
		}
	}
	if err := r.Storer.SetReference(plumbing.NewHashReference(name, newHash)); err != nil {
		return "", err
	}
	return fmt.Sprintf("pushed %s to %s (%d objects)", short(p.NewSHA), name.Short(), len(objs)), nil
}
