package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/harness"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// decideIntegration settles an approval an integration owns. ok is false for approvals
// that belong to the network policy.
func (s *Server) decideIntegration(ctx context.Context, env, id string, d integrations.Decision) (store.Approval, bool, error) {
	a, err := s.Store.Approval(ctx, env, id)
	if err != nil {
		return a, false, apiError(err)
	}
	k, ok := s.Integrations.Kind(a.Kind)
	if !ok {
		return a, false, nil
	}
	if a.Status != store.StatusPending {
		return a, true, huma.Error409Conflict("the approval was already decided")
	}
	if err := k.Decide(ctx, a, d); err != nil {
		return a, true, apiError(err)
	}
	a, err = s.Store.Approval(ctx, env, id)
	return a, true, apiError(err)
}

// gitRewrites are the managed git config's rewrites of real repository URLs to the
// virtual remote, for an environment's forges.
func (s *Server) gitRewrites(ctx context.Context, envID string) ([]harness.GitRewrite, error) {
	forges, err := s.Store.Forges(ctx, envID)
	if err != nil {
		return nil, err
	}
	var out []harness.GitRewrite
	for _, r := range gitreview.Rewrites(forges) {
		out = append(out, harness.GitRewrite{URL: r.URL, InsteadOf: r.InsteadOf})
	}
	return out, nil
}

// ForgeInput describes a forge to add.
type ForgeInput struct {
	Name     string `json:"name" pattern:"^[a-z0-9][a-z0-9-]{0,31}$" doc:"Names the forge in the remote URL: https://git.studio.internal/<name>/<path>.git"`
	Kind     string `json:"kind" enum:"forgejo,github,https,ssh"`
	BaseURL  string `json:"baseUrl" maxLength:"2048" doc:"The forge's public HTTPS URL, such as https://codeberg.org or https://github.com; for kind ssh its SSH address, such as git@host or ssh://git@host:2222"`
	Username string `json:"username,omitempty" maxLength:"64" doc:"Kind https: the user git authenticates as with the token; git if empty"`
	Token    string `json:"token,omitempty" maxLength:"4096" doc:"Every kind but ssh: an access token with read and write access to the repositories; Studio keeps it and sandboxes never see it"`
	HostKey  string `json:"hostKey,omitempty" maxLength:"16384" doc:"Kind ssh: the host key to pin, as scanned and confirmed by the user"`
}

// ForgeScanInput is an SSH forge to scan for its host key.
type ForgeScanInput struct {
	BaseURL string `json:"baseUrl" maxLength:"2048" doc:"The forge's SSH address, such as git@host or ssh://git@host:2222"`
}

// ForgeScan is a scanned host key.
type ForgeScan struct {
	BaseURL string `json:"baseUrl" doc:"The address, normalized"`
	gitreview.HostKey
}

// ForgeHostKey is a host key to pin.
type ForgeHostKey struct {
	HostKey string `json:"hostKey" minLength:"1" maxLength:"16384"`
}

// ForgeTestInput optionally names a repository to test with.
type ForgeTestInput struct {
	Repo string `json:"repo,omitempty" maxLength:"800" doc:"A repository path on the forge, such as owner/repo; required for forges without an API"`
}

// ForgeTest is the outcome of checking a forge's credential.
type ForgeTest struct {
	OK        bool       `json:"ok"`
	User      string     `json:"user,omitempty" doc:"The user the token belongs to"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty" doc:"When the token expires, if the forge says (GitHub)"`
	Repo      string     `json:"repo,omitempty" doc:"The repository tested"`
	Branches  int        `json:"branches,omitempty" doc:"How many branches it has"`
	Message   string     `json:"message,omitempty"`
}

// ForgeToken replaces a forge's token.
type ForgeToken struct {
	Token string `json:"token" minLength:"1" maxLength:"4096"`
}

func (s *Server) registerForges(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listForges", Method: http.MethodGet, Path: "/api/environments/{env}/forges", Tags: []string{"git"},
	}, func(ctx context.Context, in *envPath) (*struct{ Body []store.Forge }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Store.Forges(ctx, in.Env)
		return &struct{ Body []store.Forge }{list}, apiError(err)
	})

	type createIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body ForgeInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "createForge", Method: http.MethodPost, Path: "/api/environments/{env}/forges", Tags: []string{"git"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createIn) (*struct{ Body store.Forge }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		b := in.Body
		u, err := gitreview.CheckForge(b.Name, b.Kind, b.BaseURL, b.Username)
		if err != nil {
			return nil, apiError(err)
		}
		f := store.Forge{ID: store.NewID(), EnvironmentID: in.Env, Name: b.Name, Kind: b.Kind, BaseURL: u.Base, Username: u.User}
		prefix := "FORGE_" + strings.ToUpper(strings.ReplaceAll(b.Name, "-", "_"))
		secretName, value, note := prefix+"_TOKEN", b.Token, "Token of forge "+b.Name
		if b.Kind == gitreview.KindSSH {
			if b.Token != "" {
				return nil, huma.Error422UnprocessableEntity("ssh forges use a key Studio generates, not a token")
			}
			hk, err := gitreview.CheckHostKey(b.HostKey)
			if err != nil {
				return nil, apiError(err)
			}
			// The key exists only in memory until the vault seals it.
			key, pub, err := gitreview.GenerateKey("sandbox-studio/" + in.Env + "/" + b.Name)
			if err != nil {
				return nil, err
			}
			f.HostKey, f.PublicKey = hk.Key, pub
			secretName, value, note = prefix+"_SSH_KEY", key, "SSH key of forge "+b.Name
		} else if b.Token == "" || b.HostKey != "" {
			return nil, huma.Error422UnprocessableEntity("forges of kind " + b.Kind + " need a token and no host key")
		}
		sec, err := s.Vault.Prepare(in.Env, secretName, value, []string{u.Host}, note)
		if err != nil {
			return nil, apiError(err)
		}
		f, err = s.Store.CreateForge(ctx, f, sec)
		if err != nil {
			return nil, apiError(err)
		}
		s.Vault.Changed(in.Env)
		s.publishSecrets(in.Env)
		return &struct{ Body store.Forge }{f}, nil
	})

	type forgePath struct {
		Env string `path:"env" doc:"Environment ID"`
		ID  string `path:"id" doc:"Forge ID"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "deleteForge", Method: http.MethodDelete, Path: "/api/environments/{env}/forges/{id}", Tags: []string{"git"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Removes the forge, its token and the staged pushes to it. Pending pushes can no longer be approved.",
	}, func(ctx context.Context, in *forgePath) (*struct{}, error) {
		if err := s.Store.DeleteForge(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		if s.Git != nil {
			s.Git.Forget(in.Env, in.ID)
		}
		s.Vault.Changed(in.Env)
		s.publishSecrets(in.Env)
		return nil, nil
	})

	type testIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Forge ID"`
		Body *ForgeTestInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "testForge", Method: http.MethodPost, Path: "/api/environments/{env}/forges/{id}/test", Tags: []string{"git"},
		Description: "Checks that the forge answers and accepts Studio's credential: through its API if it has one, and by listing the branches of repo if given.",
	}, func(ctx context.Context, in *testIn) (*struct{ Body ForgeTest }, error) {
		f, err := s.Store.Forge(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		if s.Git == nil {
			return nil, huma.Error503ServiceUnavailable("the git remote is not running")
		}
		var repo string
		if in.Body != nil {
			repo = in.Body.Repo
		}
		c, err := s.Git.TestForge(ctx, f, repo)
		if err != nil {
			return &struct{ Body ForgeTest }{ForgeTest{Message: strings.TrimPrefix(fmt.Sprint(err), gitreview.ErrInvalid.Error()+": ")}}, nil
		}
		res := ForgeTest{OK: true, User: c.User, Repo: c.Repo, Branches: c.Branches}
		if !c.Expires.IsZero() {
			res.ExpiresAt = &c.Expires
		}
		return &struct{ Body ForgeTest }{res}, nil
	})

	type tokenIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Forge ID"`
		Body ForgeToken
	}
	huma.Register(api, huma.Operation{
		OperationID: "replaceForgeToken", Method: http.MethodPut, Path: "/api/environments/{env}/forges/{id}/token", Tags: []string{"git"},
		Description: "Replaces the forge's token in place, for example when it expired. Staged pushes are kept.",
	}, func(ctx context.Context, in *tokenIn) (*struct{ Body store.Forge }, error) {
		f, err := s.Store.Forge(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		if f.Kind == gitreview.KindSSH {
			return nil, huma.Error409Conflict("ssh forges have a key, not a token; to get a new key, remove the forge and add it again")
		}
		sealed, err := s.Vault.SealValue(f.SecretID, in.Body.Token)
		if err != nil {
			return nil, apiError(err)
		}
		if err := s.Store.SetForgeSecret(ctx, in.Env, f.ID, sealed); err != nil {
			return nil, apiError(err)
		}
		s.publishSecrets(in.Env)
		f, err = s.Store.Forge(ctx, in.Env, in.ID)
		return &struct{ Body store.Forge }{f}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "scanForgeHostKey", Method: http.MethodPost, Path: "/api/environments/{env}/forges/scan", Tags: []string{"git"},
		Description: "Connects to an SSH forge and returns its host key, for the user to compare with the fingerprint the forge publishes before pinning it.",
	}, func(ctx context.Context, in *struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body ForgeScanInput
	}) (*struct{ Body ForgeScan }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		if s.Git == nil {
			return nil, huma.Error503ServiceUnavailable("the git remote is not running")
		}
		u, hk, err := s.Git.ScanHostKey(ctx, in.Body.BaseURL)
		if errors.Is(err, gitreview.ErrInvalid) {
			return nil, apiError(err)
		}
		if err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		return &struct{ Body ForgeScan }{ForgeScan{BaseURL: u.Base, HostKey: hk}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "pinForgeHostKey", Method: http.MethodPut, Path: "/api/environments/{env}/forges/{id}/host-key", Tags: []string{"git"},
		Description: "Pins another host key for an SSH forge, after the user confirmed it with the host's admin.",
	}, func(ctx context.Context, in *struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Forge ID"`
		Body ForgeHostKey
	}) (*struct{ Body store.Forge }, error) {
		f, err := s.Store.Forge(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		if f.Kind != gitreview.KindSSH {
			return nil, huma.Error409Conflict("only ssh forges have a host key")
		}
		hk, err := gitreview.CheckHostKey(in.Body.HostKey)
		if err != nil {
			return nil, apiError(err)
		}
		if err := s.Store.PinForgeHostKey(ctx, in.Env, f.ID, hk.Key); err != nil {
			return nil, apiError(err)
		}
		f, err = s.Store.Forge(ctx, in.Env, in.ID)
		return &struct{ Body store.Forge }{f}, apiError(err)
	})
}
