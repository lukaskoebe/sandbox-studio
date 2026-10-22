package gitreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// KindCredential is a card for a host a sandbox's git asked credentials for: no forge covers
// it, so git has none. Allowing it means the user went to add the forge; dismissing it keeps
// Studio from asking about the host again for a while.
const KindCredential = "git.credential"

// credentialSnooze is how long "Not now" keeps Studio from asking about a host again.
const credentialSnooze = 24 * time.Hour

// GitCredentialPayload is what a git.credential card shows.
type GitCredentialPayload struct {
	TeammateID   string `json:"teammateId,omitempty"`
	TeammateName string `json:"teammateName,omitempty"`
	SandboxID    string `json:"sandboxId"`
	SandboxName  string `json:"sandboxName"`
	Host         string `json:"host" doc:"The host git asked credentials for, with its port when git named one"`
	URL          string `json:"url" doc:"The repository's URL, for the Add forge dialog"`
	Kind         string `json:"kind,omitempty" enum:"forgejo,github,gitlab,https" doc:"The kind of forge the URL suggests"`
}

// CredentialAnswer is the git_credential answer: a line for the helper to print to git's
// stderr, where the agent reads it. Studio never answers with a credential.
type CredentialAnswer struct {
	Message string `json:"message,omitempty"`
}

// credential answers the git credential helper (`studio-agent git-credential get`). It
// posts a card offering to add a forge for the host, unless one covers it already or the
// user said not now; the helper hands git nothing either way, so the command fails as it
// would have.
func (s *Service) credential(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		Protocol string `json:"protocol"`
		Host     string `json:"host"`
		Path     string `json:"path"`
	}
	if err := decodeArgs(params, &in); err != nil {
		return nil, err
	}
	host := strings.ToLower(in.Host)
	u, err := url.Parse("https://" + host + "/")
	if in.Protocol != "https" || err != nil || u.Hostname() == "" || u.User != nil || u.Host != host || len(host) > 260 {
		return CredentialAnswer{}, nil
	}
	name := u.Hostname()
	if name == Host || strings.HasSuffix(name, ".studio.internal") {
		return CredentialAnswer{}, nil
	}
	path := strings.Trim(in.Path, "/")
	if len(path) > 500 || strings.ContainsFunc(path, func(r rune) bool { return r < 0x21 || r == 0x7f }) {
		path = ""
	}
	forges, err := s.Store.Forges(ctx, sb.EnvironmentID)
	if err != nil {
		return nil, err
	}
	for _, f := range forges {
		if h, _ := rewriteRules(f); strings.EqualFold(h, name) {
			return CredentialAnswer{fmt.Sprintf("Sandbox Studio: %s is the forge %q in Studio. Use the repository's https://%s/ URL, "+
				"which Studio sends through its git remote.", host, f.Name, host)}, nil
		}
	}
	key := sb.EnvironmentID + "/" + host
	s.credMu.Lock()
	until, snoozed := s.snoozed[key]
	s.credMu.Unlock()
	if snoozed && time.Now().Before(until) {
		return CredentialAnswer{fmt.Sprintf("Sandbox Studio: the user chose not to add access to %s for now. "+
			"Don't ask again; tell them the command needs it.", host)}, nil
	}

	who := sb.Name
	p := GitCredentialPayload{SandboxID: sb.ID, SandboxName: sb.Name, Host: host, URL: "https://" + host + "/"}
	if path != "" {
		p.URL += path
	}
	if sb.TeammateID != "" {
		tm, err := s.Store.Teammate(ctx, sb.EnvironmentID, sb.TeammateID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if err == nil {
			p.TeammateID, p.TeammateName, who = tm.ID, tm.Name, tm.Name
		}
	}
	if r, err := ParseRepoURL(p.URL); err == nil && r.Kind != KindSSH && r.Kind != KindLocal {
		p.Kind = r.Kind
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	a, _, err := s.Store.RequestApproval(ctx, store.Approval{
		EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, Kind: KindCredential,
		Subject: fmt.Sprintf("%s needs access to %s", who, host), Payload: raw,
	})
	if err != nil {
		return nil, err
	}
	s.publish(sb.EnvironmentID, a.ID)
	return CredentialAnswer{fmt.Sprintf("Sandbox Studio: git has no credentials for %s, because no forge in Studio covers it. "+
		"The user has a card to add one; once they have, run the command again. Don't retry it before then.", host)}, nil
}

// decideCredential settles a git.credential card. Allowing it only clears it, since the web
// goes on to the Add forge dialog; the other decisions also keep Studio from asking about
// the host again for a day.
func (s *Service) decideCredential(ctx context.Context, a store.Approval, d integrations.Decision) error {
	st, err := status(d.Action)
	if err != nil {
		return err
	}
	var p GitCredentialPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return err
	}
	if err := s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, st, ""); err != nil {
		return err
	}
	if st != store.StatusApproved {
		s.credMu.Lock()
		if s.snoozed == nil {
			s.snoozed = map[string]time.Time{}
		}
		s.snoozed[a.EnvironmentID+"/"+p.Host] = time.Now().Add(credentialSnooze)
		s.credMu.Unlock()
	}
	s.publish(a.EnvironmentID, a.ID)
	return nil
}
