package browser

import (
	"context"
	"net/url"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Blocked is the network rule that refused the browser's last connection to its page's host,
// so the Browser page can say why the page didn't load and link to the rule.
type Blocked struct {
	Host     string `json:"host" doc:"The page's host"`
	RuleID   string `json:"ruleId"`
	RuleHost string `json:"ruleHost" doc:"The rule's host pattern, such as *.example.com"`
}

// blocked finds the deny rule behind a page that didn't load: the browser VM's newest
// connection to the page's host was refused by it. A rule since deleted, or a newer connection
// the gateway let through, blocks nothing.
func (s *Service) blocked(ctx context.Context, vm store.Sandbox, page string) *Blocked {
	if s.Conns == nil || vm.ID == "" || page == "" {
		return nil
	}
	u, err := url.Parse(page)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	for _, c := range s.Conns.List(vm.ID) {
		if !strings.EqualFold(c.Host, host) {
			continue
		}
		if c.Verdict != gateway.VerdictDenied || c.RuleID == "" {
			return nil
		}
		r, err := s.Store.Rule(ctx, vm.EnvironmentID, c.RuleID)
		if err != nil || r.Action != store.ActionDeny {
			return nil
		}
		return &Blocked{Host: host, RuleID: r.ID, RuleHost: r.Host}
	}
	return nil
}
