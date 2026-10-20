package api

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/harness"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// GitIdentity is a git author.
type GitIdentity struct {
	Name  string `json:"name" doc:"user.name; empty if it is not set"`
	Email string `json:"email" doc:"user.email; empty if it is not set"`
}

// HostGitIdentity reads the git identity of the user Studio runs as: git config --global
// user.name and user.email. A value that is not set, or a host without git, reads as empty.
func HostGitIdentity(ctx context.Context) GitIdentity {
	get := func(key string) string {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "git", "config", "--global", "--get", key).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return GitIdentity{Name: get("user.name"), Email: get("user.email")}
}

// hostGitIdentity is the host's git identity, or an empty one when the server has no reader.
func (s *Server) hostGitIdentity(ctx context.Context) GitIdentity {
	if s.HostGit == nil {
		return GitIdentity{}
	}
	return s.HostGit(ctx)
}

// sessionGitIdentity is the identity p's sessions commit with: the host's when p uses it and
// the host has a name and an email git config can hold, else p's own.
func (s *Server) sessionGitIdentity(ctx context.Context, p store.Teammate) (name, email string) {
	if p.UseHostGitIdentity {
		host := s.hostGitIdentity(ctx)
		if host.Name != "" && host.Email != "" && teammates.CheckGitName(host.Name) == nil && teammates.CheckGitEmail(host.Email) == nil {
			return host.Name, host.Email
		}
	}
	return p.GitName, p.GitEmail
}

// harnessTeammate is what the harness adapters get of p.
func (s *Server) harnessTeammate(ctx context.Context, p store.Teammate) harness.Teammate {
	name, email := s.sessionGitIdentity(ctx, p)
	return harness.Teammate{Name: p.Name, Role: p.Role, Soul: p.Soul, Model: p.Model, GitName: name, GitEmail: email}
}

func (s *Server) registerGitIdentity(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "getHostGitIdentity", Method: http.MethodGet, Path: "/api/system/git-identity", Tags: []string{"system"},
		Summary:     "The git identity of the user Studio runs as",
		Description: "git config --global user.name and user.email on the Studio host, which teammates that use the host's identity commit with. Values that are not set are empty.",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body GitIdentity }, error) {
		return &struct{ Body GitIdentity }{s.hostGitIdentity(ctx)}, nil
	})
}
