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

func forgeSecretPrefix(name string) string {
	return "FORGE_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// forgeKey generates an SSH key for a forge and prepares its studio-only secret. It returns
// the public key, for the user to add to the host.
func (s *Server) forgeKey(env, name, host string) (string, store.Secret, error) {
	// The key exists only in memory until the vault seals it.
	key, pub, err := gitreview.GenerateKey("sandbox-studio/" + env + "/" + name)
	if err != nil {
		return "", store.Secret{}, err
	}
	sec, err := s.Vault.Prepare(env, forgeSecretPrefix(name)+"_SSH_KEY", key, []string{host}, "SSH key of forge "+name)
	return pub, sec, apiError(err)
}

// ForgeInput describes a forge to add.
type ForgeInput struct {
	Name     string `json:"name" pattern:"^[a-z0-9][a-z0-9-]{0,31}$" doc:"Names the forge in the remote URL: https://git.studio.internal/<name>/<path>.git"`
	Kind     string `json:"kind" enum:"forgejo,github,gitlab,https,ssh,local"`
	BaseURL  string `json:"baseUrl" maxLength:"4096" doc:"The forge's public HTTPS URL, such as https://codeberg.org or https://github.com; for kind ssh its SSH address, such as git@host or ssh://git@host:2222; for kind local a folder of repositories on this computer, such as ~/git"`
	Username string `json:"username,omitempty" maxLength:"64" doc:"Kind https: the user git authenticates as with the token; git if empty"`
	Token    string `json:"token,omitempty" maxLength:"4096" doc:"Every kind but ssh and local: an access token with read and write access to the repositories; for kind ssh with apiKind, the API's token. Studio keeps it and sandboxes never see it"`
	HostKey  string `json:"hostKey,omitempty" maxLength:"16384" doc:"Kind ssh: the host key to pin, as scanned and confirmed by the user"`
	Auth     string `json:"auth,omitempty" enum:"agent,key" doc:"Kind ssh: agent (the default) signs with the host's SSH agent; key makes Studio generate a key for the forge"`
	APIKind  string `json:"apiKind,omitempty" enum:"forgejo,github,gitlab" doc:"Kind ssh: the forge's API, for repository lists and pull requests, reached with token; git still goes over SSH"`
	APIURL   string `json:"apiUrl,omitempty" maxLength:"4096" doc:"Kind ssh with apiKind: the forge's HTTPS URL, such as https://codeberg.org"`
	// The check's failure is a 422 at body.skipCheck, so the form can offer to add it anyway.
	SkipCheck bool `json:"skipCheck,omitempty" doc:"Add the forge without first checking that its API answers at the URL and accepts the token, as for a forge Studio can't reach yet"`
}

// ForgeAPI gives an ssh forge an API, for repository lists and pull requests.
type ForgeAPI struct {
	Kind    string `json:"kind" enum:"forgejo,github,gitlab"`
	BaseURL string `json:"baseUrl" maxLength:"4096" doc:"The forge's HTTPS URL, such as https://codeberg.org"`
	Token   string `json:"token" minLength:"1" maxLength:"4096" doc:"An access token for the API; Studio keeps it and sandboxes never see it"`
	// The check's failure is a 422 at body.skipCheck, as for a new forge.
	SkipCheck bool `json:"skipCheck,omitempty" doc:"Save the API without first checking that it answers at the URL and accepts the token"`
}

// ForgeAuth switches how an ssh forge authenticates.
type ForgeAuth struct {
	Auth string `json:"auth" enum:"agent,key"`
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
		secretName, value, note := forgeSecretPrefix(b.Name)+"_TOKEN", b.Token, "Token of forge "+b.Name
		var sec, apiSec store.Secret
		if b.APIKind != "" || b.APIURL != "" {
			if b.Kind != gitreview.KindSSH {
				return nil, huma.Error422UnprocessableEntity("only ssh forges take an API; forges of kind " + b.Kind + " have their own or none")
			}
			if b.Token == "" {
				return nil, huma.Error422UnprocessableEntity("an ssh forge's API needs a token")
			}
			apiURL, apiHost, err := gitreview.CheckAPI(b.APIKind, b.APIURL)
			if err != nil {
				return nil, apiError(err)
			}
			if !b.SkipCheck {
				if err := s.checkForgeToken(ctx, b.APIKind, apiURL, b.Token); err != nil {
					return nil, err
				}
			}
			f.APIKind, f.APIURL = b.APIKind, apiURL
			if apiSec, err = s.Vault.Prepare(in.Env, secretName, value, []string{apiHost}, "API token of forge "+b.Name); err != nil {
				return nil, apiError(err)
			}
		}
		if b.Kind == gitreview.KindSSH {
			if b.Token != "" && b.APIKind == "" {
				return nil, huma.Error422UnprocessableEntity("ssh forges sign with the SSH agent or a key Studio generates; a token is for their API, with apiKind")
			}
			hk, err := gitreview.CheckHostKey(b.HostKey)
			if err == nil {
				_, err = gitreview.CheckPublished(u, hk)
			}
			if err != nil {
				return nil, apiError(err)
			}
			f.HostKey, f.Auth = hk.Key, b.Auth
			if f.Auth == "" {
				f.Auth = store.ForgeAuthAgent
			}
			if f.Auth == store.ForgeAuthKey {
				if f.PublicKey, sec, err = s.forgeKey(in.Env, b.Name, u.Host); err != nil {
					return nil, err
				}
			}
		} else if b.Kind == gitreview.KindLocal {
			if b.Token != "" || b.HostKey != "" || b.Auth != "" {
				return nil, huma.Error422UnprocessableEntity("local forges are folders on this computer: no token, host key or auth")
			}
			f.Auth = store.ForgeAuthNone
		} else if b.Token == "" || b.HostKey != "" || b.Auth != "" {
			return nil, huma.Error422UnprocessableEntity("forges of kind " + b.Kind + " need a token, and no host key or auth")
		} else {
			if gitreview.HasAPI(f) && !b.SkipCheck {
				if err := s.checkForgeToken(ctx, b.Kind, u.Base, b.Token); err != nil {
					return nil, err
				}
			}
			if sec, err = s.Vault.Prepare(in.Env, secretName, value, []string{u.Host}, note); err != nil {
				return nil, apiError(err)
			}
		}
		if f.APIKind != "" {
			f, err = s.Store.CreateForgeWithAPI(ctx, f, sec, apiSec)
		} else {
			f, err = s.Store.CreateForge(ctx, f, sec)
		}
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
		if f.Kind == gitreview.KindSSH && f.APISecretID == "" {
			return nil, huma.Error409Conflict("ssh forges have a key, not a token; give the forge an API to add a token for it")
		}
		if f.Kind == gitreview.KindLocal {
			return nil, huma.Error409Conflict("local forges have no token")
		}
		// An ssh forge's token is its API's.
		secretID, set := f.SecretID, s.Store.SetForgeSecret
		if f.Kind == gitreview.KindSSH {
			secretID, set = f.APISecretID, s.Store.SetForgeAPISecret
		}
		sealed, err := s.Vault.SealValue(secretID, in.Body.Token)
		if err != nil {
			return nil, apiError(err)
		}
		if err := set(ctx, in.Env, f.ID, sealed); err != nil {
			return nil, apiError(err)
		}
		s.publishSecrets(in.Env)
		f, err = s.Store.Forge(ctx, in.Env, in.ID)
		return &struct{ Body store.Forge }{f}, apiError(err)
	})

	type apiIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Forge ID"`
		Body ForgeAPI
	}
	huma.Register(api, huma.Operation{
		OperationID: "setForgeAPI", Method: http.MethodPut, Path: "/api/environments/{env}/forges/{id}/api", Tags: []string{"git"},
		Description: "Gives an ssh forge an API with a token, or replaces the one it has: Studio then lists its repositories and reads, reviews and opens its pull requests through the API, while git still goes over SSH.",
	}, func(ctx context.Context, in *apiIn) (*struct{ Body store.Forge }, error) {
		f, err := s.Store.Forge(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		if f.Kind != gitreview.KindSSH {
			return nil, huma.Error409Conflict("only ssh forges take an API; forges of kind " + f.Kind + " have their own or none")
		}
		apiURL, apiHost, err := gitreview.CheckAPI(in.Body.Kind, in.Body.BaseURL)
		if err != nil {
			return nil, apiError(err)
		}
		if !in.Body.SkipCheck {
			if err := s.checkForgeToken(ctx, in.Body.Kind, apiURL, in.Body.Token); err != nil {
				return nil, err
			}
		}
		sec, err := s.Vault.Prepare(in.Env, forgeSecretPrefix(f.Name)+"_TOKEN", in.Body.Token, []string{apiHost}, "API token of forge "+f.Name)
		if err != nil {
			return nil, apiError(err)
		}
		if err := s.Store.SetForgeAPI(ctx, in.Env, f.ID, in.Body.Kind, apiURL, sec); err != nil {
			return nil, apiError(err)
		}
		s.Vault.Changed(in.Env)
		s.publishSecrets(in.Env)
		f, err = s.Store.Forge(ctx, in.Env, in.ID)
		return &struct{ Body store.Forge }{f}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "removeForgeAPI", Method: http.MethodDelete, Path: "/api/environments/{env}/forges/{id}/api", Tags: []string{"git"},
		Description: "Takes an ssh forge's API away and deletes its token. git over SSH keeps working; pull requests and repository lists stop.",
	}, func(ctx context.Context, in *forgePath) (*struct{ Body store.Forge }, error) {
		f, err := s.Store.Forge(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		if f.Kind != gitreview.KindSSH {
			return nil, huma.Error409Conflict("only ssh forges take an API; forges of kind " + f.Kind + " have their own or none")
		}
		if err := s.Store.RemoveForgeAPI(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		s.Vault.Changed(in.Env)
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
		if err == nil {
			var u gitreview.ForgeURL
			if u, err = gitreview.ParseSSHURL(f.BaseURL); err == nil {
				_, err = gitreview.CheckPublished(u, hk)
			}
		}
		if err != nil {
			return nil, apiError(err)
		}
		if err := s.Store.PinForgeHostKey(ctx, in.Env, f.ID, hk.Key); err != nil {
			return nil, apiError(err)
		}
		f, err = s.Store.Forge(ctx, in.Env, in.ID)
		return &struct{ Body store.Forge }{f}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "parseForgeURL", Method: http.MethodPost, Path: "/api/environments/{env}/forges/parse-url", Tags: []string{"git"},
		Description: "Describes the forge for a pasted repository URL (git@host:owner/repo.git, ssh://… or https://…), with a short name not yet taken in the environment. " +
			"Without detect, only hosts Studio knows, such as github.com and codeberg.org, get their API.",
	}, func(ctx context.Context, in *struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			URL    string `json:"url" minLength:"1" maxLength:"2048"`
			Detect bool   `json:"detect,omitempty" doc:"Also ask a host Studio doesn't know which forge it runs (Forgejo or Gitea, GitLab, GitHub Enterprise), by endpoints they answer without a token; takes up to a few seconds"`
		}
	}) (*struct{ Body gitreview.RepoURL }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		forges, err := s.Store.Forges(ctx, in.Env)
		if err != nil {
			return nil, apiError(err)
		}
		parse := func(ctx context.Context, raw string) (gitreview.RepoURL, error) { return gitreview.ParseRepoURL(raw) }
		if in.Body.Detect {
			if s.Git == nil {
				return nil, huma.Error503ServiceUnavailable("the git remote is not running")
			}
			parse = s.Git.DetectRepoURL
		}
		r, err := parse(ctx, in.Body.URL)
		if err != nil {
			return nil, apiError(err)
		}
		r.Name = gitreview.FreeName(r.Name, forges)
		return &struct{ Body gitreview.RepoURL }{r}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "setForgeAuth", Method: http.MethodPut, Path: "/api/environments/{env}/forges/{id}/auth", Tags: []string{"git"},
		Description: "Switches an SSH forge between the host's SSH agent and a key Studio generates. Switching to a key returns its public key; switching to the agent deletes the key.",
	}, func(ctx context.Context, in *struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Forge ID"`
		Body ForgeAuth
	}) (*struct{ Body store.Forge }, error) {
		f, err := s.Store.Forge(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		if f.Kind != gitreview.KindSSH {
			return nil, huma.Error409Conflict("only ssh forges can use the SSH agent")
		}
		if f.Auth != in.Body.Auth {
			if in.Body.Auth == store.ForgeAuthKey {
				u, err := gitreview.ParseSSHURL(f.BaseURL)
				if err != nil {
					return nil, apiError(err)
				}
				pub, sec, err := s.forgeKey(in.Env, f.Name, u.Host)
				if err != nil {
					return nil, err
				}
				if err := s.Store.SetForgeKey(ctx, in.Env, f.ID, pub, sec); err != nil {
					return nil, apiError(err)
				}
			} else if err := s.Store.SetForgeAgent(ctx, in.Env, f.ID); err != nil {
				return nil, apiError(err)
			}
			s.Vault.Changed(in.Env)
			s.publishSecrets(in.Env)
		}
		f, err = s.Store.Forge(ctx, in.Env, in.ID)
		return &struct{ Body store.Forge }{f}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "dismissForgeAgentHint", Method: http.MethodPost, Path: "/api/environments/{env}/forges/{id}/dismiss-agent-hint", Tags: []string{"git"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Hides the suggestion to use a Studio key after the SSH agent asked for confirmation.",
	}, func(ctx context.Context, in *forgePath) (*struct{}, error) {
		return nil, apiError(s.Store.DismissForgeAgentHint(ctx, in.Env, in.ID))
	})

	huma.Register(api, huma.Operation{
		OperationID: "getSSHAgent", Method: http.MethodGet, Path: "/api/system/ssh-agent", Tags: []string{"git"},
		Summary:     "Check the host's SSH agent",
		Description: "Whether Studio reaches the SSH agent it signs with for SSH forges, and the keys the agent holds.",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body gitreview.AgentStatus }, error) {
		if s.Git == nil {
			return nil, huma.Error503ServiceUnavailable("the git remote is not running")
		}
		return &struct{ Body gitreview.AgentStatus }{s.Git.AgentStatus(ctx)}, nil
	})
}

// checkForgeToken checks that the API of kind at baseURL accepts token before Studio keeps
// them. Its failure is a 422 at body.skipCheck: the form then offers to save without the check.
func (s *Server) checkForgeToken(ctx context.Context, kind, baseURL, token string) error {
	if s.Git == nil {
		return huma.Error503ServiceUnavailable("the git remote is not running, so the forge's API can't be checked; skip the check to save it anyway")
	}
	if _, err := s.Git.CheckToken(ctx, kind, baseURL, token); err != nil {
		msg := strings.TrimPrefix(err.Error(), gitreview.ErrInvalid.Error()+": ")
		return huma.Error422UnprocessableEntity("Studio could not use the API at "+baseURL+" with this token: "+msg,
			&huma.ErrorDetail{Location: "body.skipCheck", Message: msg})
	}
	return nil
}
