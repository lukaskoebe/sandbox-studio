package browser

import (
	"net/url"
	"strings"
	"sync"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// SignIn is a page of the browser asking for a username and password (HTTP Basic), which the
// browser can't ask the user for itself: the Browser page offers to save a browser login.
type SignIn struct {
	Host  string `json:"host" doc:"The page's host"`
	Realm string `json:"realm,omitempty" doc:"What the host says the credentials are for"`
	// Login is the teammate's browser login for the host: the one Studio sent and the host
	// refused, or one held back as the agent driving may not use it.
	Login   string `json:"login,omitempty" doc:"The teammate's browser login for the host, which Studio sent (refused) or held back from the agent"`
	Refused bool   `json:"refused" doc:"The host refused the login Studio sent"`
}

// maxSignIns caps the hosts asking that are kept per browser VM.
const maxSignIns = 64

// signIns are the hosts whose pages last asked for Basic credentials, by browser VM.
type signIns struct {
	mu   sync.Mutex
	byVM map[string]map[string]SignIn
}

// SignInSeen is the gateway's SignIn: whether a page of browser VM vm on host asks for Basic
// credentials.
func (s *Service) SignInSeen(vm store.Sandbox, host string, ask *gateway.BasicAsk) {
	host = strings.ToLower(host)
	s.signIns.mu.Lock()
	defer s.signIns.mu.Unlock()
	hosts := s.signIns.byVM[vm.ID]
	if ask == nil {
		delete(hosts, host)
		return
	}
	if hosts == nil {
		if s.signIns.byVM == nil {
			s.signIns.byVM = map[string]map[string]SignIn{}
		}
		hosts = map[string]SignIn{}
		s.signIns.byVM[vm.ID] = hosts
	}
	if _, ok := hosts[host]; !ok && len(hosts) >= maxSignIns {
		return
	}
	hosts[host] = SignIn{Host: host, Realm: ask.Realm, Login: ask.Login, Refused: ask.Refused}
}

// signIn is the sign-in a page of browser VM vm asks for, if it does.
func (s *Service) signIn(vm store.Sandbox, page string) *SignIn {
	if vm.ID == "" || page == "" {
		return nil
	}
	u, err := url.Parse(page)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil
	}
	s.signIns.mu.Lock()
	defer s.signIns.mu.Unlock()
	if in, ok := s.signIns.byVM[vm.ID][strings.ToLower(u.Hostname())]; ok {
		return &in
	}
	return nil
}
