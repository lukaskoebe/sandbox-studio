package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

// An environment file is an environment's configuration as JSON: providers without keys,
// teammates, rules, secret names with the hosts they bind to, and template specs. Sandboxes,
// memory, approvals, chats and every key or secret value stay behind. The file is untrusted:
// it is read with a size limit, unknown fields are refused and every value is checked.
const (
	environmentFormat       = "studio-environment"
	environmentVersion      = 1
	environmentFileExt      = ".studio-environment"
	maxEnvironmentFileBytes = 2 << 20
	maxFileProviders        = 32
	maxFileTeammates        = 64
	maxFileRules            = 500
	maxFileSecrets          = 500
	maxFileTemplates        = 32
)

// EnvironmentFile is the content of a .studio-environment file.
type EnvironmentFile struct {
	Format     string         `json:"format" enum:"studio-environment"`
	Version    int            `json:"version" enum:"1"`
	Name       string         `json:"name" maxLength:"64" doc:"The environment's name; an import gets a number if it is taken"`
	ExportedAt time.Time      `json:"exportedAt"`
	Providers  []FileProvider `json:"providers" maxItems:"32" doc:"LLM providers, without their keys"`
	Teammates  []FileTeammate `json:"teammates" maxItems:"64"`
	Rules      []FileRule     `json:"rules" maxItems:"500" doc:"Network rules of the environment and of its teammates"`
	Secrets    []FileSecret   `json:"secrets" maxItems:"500" doc:"Secret names and the hosts they bind to, without values"`
	Templates  []FileTemplate `json:"templates" maxItems:"32"`
}

// FileProvider is a provider without its key.
type FileProvider struct {
	Name    string `json:"name" minLength:"1" maxLength:"64"`
	Kind    string `json:"kind" enum:"anthropic_api,openai_api,openai_compatible,claude_subscription,chatgpt_subscription"`
	BaseURL string `json:"baseUrl,omitempty" maxLength:"2048" doc:"openai_compatible only"`
	Model   string `json:"model,omitempty" maxLength:"128" doc:"openai_compatible only: the endpoint's single model"`
}

// FileTeammate is a teammate. Provider names a provider of the file.
type FileTeammate struct {
	Name          string `json:"name" minLength:"1" maxLength:"64"`
	Role          string `json:"role,omitempty" maxLength:"200"`
	Avatar        string `json:"avatar,omitempty" maxLength:"64"`
	ApprovalScope string `json:"approvalScope,omitempty" enum:"sandbox,teammate,environment"`
	Soul          string `json:"soul,omitempty" maxLength:"16384"`
	Harness       string `json:"harness" enum:"opencode,claude,codex"`
	Provider      string `json:"provider,omitempty" maxLength:"64" doc:"The name of a provider of this file; empty if the teammate has none"`
	Model         string `json:"model,omitempty" maxLength:"128"`
	GitName       string `json:"gitName,omitempty" maxLength:"100"`
	GitEmail      string `json:"gitEmail,omitempty" maxLength:"254"`
}

// FileRule is a network rule. Teammate names a teammate of the file; empty means the whole
// environment. Rules scoped to one sandbox are not exported.
type FileRule struct {
	Host       string            `json:"host" minLength:"1" maxLength:"253"`
	Ports      []int             `json:"ports,omitempty" maxItems:"64"`
	Action     string            `json:"action" enum:"allow,proxy,caddy,deny"`
	Headers    map[string]string `json:"headers,omitempty" maxProperties:"32"`
	Caddyfile  string            `json:"caddyfile,omitempty" maxLength:"32768"`
	Upstream   string            `json:"upstream,omitempty" maxLength:"261"`
	HostHeader string            `json:"hostHeader,omitempty" maxLength:"253"`
	SNI        string            `json:"sni,omitempty" maxLength:"253"`
	CA         string            `json:"ca,omitempty" maxLength:"65536"`
	Note       string            `json:"note,omitempty" maxLength:"500"`
	Teammate   string            `json:"teammate,omitempty" maxLength:"64"`
}

// FileSecret is a secret without its value: the name a sandbox sees, the hosts it may be sent
// to, and the teammate it is scoped to (empty for the environment).
type FileSecret struct {
	Name     string   `json:"name" minLength:"1" maxLength:"64"`
	Hosts    []string `json:"hosts" minItems:"1" maxItems:"32"`
	Note     string   `json:"note,omitempty" maxLength:"500"`
	Teammate string   `json:"teammate,omitempty" maxLength:"64"`
}

// FileTemplate is a template specification, in YAML.
type FileTemplate struct {
	Spec string `json:"spec" minLength:"1" maxLength:"65536"`
}

// ImportedEnvironment is an import's result: the new environment, and what the file could not
// carry: the keys of its providers, the values of its secrets, and its templates to build.
type ImportedEnvironment struct {
	Environment store.Environment `json:"environment"`
	ToAdd       []ToAdd           `json:"toAdd" doc:"Provider keys and secret values to add"`
	Templates   []string          `json:"templates" doc:"Template specifications of the file, in YAML, to build"`
}

// ToAdd is one thing an import leaves for the user to supply.
type ToAdd struct {
	Kind       string   `json:"kind" enum:"providerKey,secret"`
	Name       string   `json:"name"`
	ProviderID string   `json:"providerId,omitempty" doc:"For providerKey: the provider to add the key to"`
	Hosts      []string `json:"hosts,omitempty" doc:"For secret: the hosts the value may be sent to"`
	Teammate   string   `json:"teammate,omitempty" doc:"For secret: the teammate it is scoped to"`
}

// buildEnvironmentFile returns the file for environment envID.
func (s *Server) buildEnvironmentFile(ctx context.Context, envID string) (EnvironmentFile, error) {
	env, err := s.Store.Environment(ctx, envID)
	if err != nil {
		return EnvironmentFile{}, err
	}
	providers, err := s.Store.Providers(ctx, envID)
	if err != nil {
		return EnvironmentFile{}, err
	}
	list, err := s.Store.Teammates(ctx, envID)
	if err != nil {
		return EnvironmentFile{}, err
	}
	rules, err := s.Store.Rules(ctx, envID)
	if err != nil {
		return EnvironmentFile{}, err
	}
	secretList, err := s.Store.Secrets(ctx, envID)
	if err != nil {
		return EnvironmentFile{}, err
	}
	templates, err := s.Store.Templates(ctx, envID)
	if err != nil {
		return EnvironmentFile{}, err
	}

	f := EnvironmentFile{
		Format: environmentFormat, Version: environmentVersion, Name: env.Name,
		ExportedAt: time.Now().UTC().Truncate(time.Second),
		Providers:  []FileProvider{}, Teammates: []FileTeammate{}, Rules: []FileRule{}, Secrets: []FileSecret{}, Templates: []FileTemplate{},
	}
	providerName := map[string]string{}
	providerKeys := map[string]bool{}
	for _, p := range providers {
		providerName[p.ID] = p.Name
		if p.SecretID != "" {
			providerKeys[p.SecretID] = true
		}
		f.Providers = append(f.Providers, FileProvider{Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL, Model: p.Model})
	}
	teammateName := map[string]string{}
	for _, t := range list {
		teammateName[t.ID] = t.Name
	}
	for _, t := range list {
		f.Teammates = append(f.Teammates, FileTeammate{
			Name: t.Name, Role: t.Role, Avatar: t.Avatar, ApprovalScope: t.ApprovalScope, Soul: t.Soul, Harness: t.Harness,
			Provider: providerName[t.ProviderID], Model: t.Model, GitName: t.GitName, GitEmail: t.GitEmail,
		})
	}
	for _, r := range rules {
		if r.SandboxID != "" {
			continue
		}
		var ports []int
		if len(r.Ports) > 0 {
			ports = r.Ports
		}
		f.Rules = append(f.Rules, FileRule{
			Host: r.Host, Ports: ports, Action: r.Action, Headers: r.Config.Headers, Caddyfile: r.Config.Caddyfile,
			Upstream: r.Config.Upstream, HostHeader: r.Config.HostHeader, SNI: r.Config.SNI, CA: r.Config.CA,
			Note: r.Note, Teammate: teammateName[r.TeammateID],
		})
	}
	for _, sec := range secretList {
		// Studio's own secrets, sandbox-scoped ones and provider keys don't travel.
		if sec.StudioOnly || sec.SandboxID != "" || providerKeys[sec.ID] {
			continue
		}
		f.Secrets = append(f.Secrets, FileSecret{Name: sec.Name, Hosts: sec.Hosts, Note: sec.Note, Teammate: teammateName[sec.TeammateID]})
	}
	seen := map[string]bool{}
	for _, tpl := range templates {
		spec, err := templatespec.ParseCanonicalJSON([]byte(tpl.Spec))
		if err != nil {
			s.Log.Warn("environment export: skipping a template with an unreadable spec", "environment", envID, "err", err)
			continue
		}
		text, err := templatespec.YAML(spec)
		if err != nil {
			return EnvironmentFile{}, err
		}
		if !seen[string(text)] {
			seen[string(text)] = true
			f.Templates = append(f.Templates, FileTemplate{Spec: string(text)})
		}
	}
	return f, nil
}

// exportEnvironment streams the environment file of an environment as a download.
func (s *Server) exportEnvironment(w http.ResponseWriter, r *http.Request) {
	f, err := s.buildEnvironmentFile(r.Context(), r.PathValue("env"))
	if err != nil {
		s.writeProblem(w, "environment export failed", apiError(err))
		return
	}
	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		s.writeProblem(w, "environment export failed", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fileSafeName(f.Name) + environmentFileExt}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
}

// fileSafeName keeps the letters, digits, spaces and . _ - of name, for a download's file name.
func fileSafeName(name string) string {
	out := []rune(name)
	for i, c := range out {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ' ' || c == '.' || c == '_' || c == '-') {
			out[i] = '-'
		}
	}
	if len(out) == 0 {
		return "environment"
	}
	return string(out)
}

// importEnvironment creates an environment from an uploaded environment file.
func (s *Server) importEnvironment(w http.ResponseWriter, r *http.Request) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		s.writeProblem(w, "", huma.Error415UnsupportedMediaType("the body must be application/json"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEnvironmentFileBytes))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		s.writeProblem(w, "", huma.NewError(http.StatusRequestEntityTooLarge, "the environment file is larger than 2 MiB"))
		return
	case err != nil:
		s.writeProblem(w, "", huma.Error400BadRequest("the environment file could not be read"))
		return
	}
	f, err := parseEnvironmentFile(body)
	if err != nil {
		s.writeProblem(w, "", huma.Error422UnprocessableEntity(err.Error()))
		return
	}
	imported, err := s.createEnvironmentFrom(r.Context(), f)
	if err != nil {
		s.writeProblem(w, "environment import failed", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", "/api/environments/"+imported.Environment.ID)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(imported)
}

// parseEnvironmentFile decodes an environment file, refusing unknown fields and trailing data.
func parseEnvironmentFile(data []byte) (EnvironmentFile, error) {
	var f EnvironmentFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return f, fmt.Errorf("not a valid environment file: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return f, errors.New("not a valid environment file: data after the JSON object")
	}
	if f.Format != environmentFormat || f.Version != environmentVersion {
		return f, fmt.Errorf("not a Studio environment file of version %d", environmentVersion)
	}
	return f, nil
}

// importPlan is a checked environment file: the records to create, and what to supply.
type importPlan struct {
	name      string
	providers []store.Provider
	teammates []store.Teammate
	rules     []store.Rule
	toAdd     []ToAdd
	templates []string
}

// createEnvironmentFrom checks f and creates its environment in one transaction.
func (s *Server) createEnvironmentFrom(ctx context.Context, f EnvironmentFile) (ImportedEnvironment, error) {
	plan, err := s.checkEnvironmentFile(f)
	if err != nil {
		return ImportedEnvironment{}, err
	}
	existing, err := s.Store.Environments(ctx)
	if err != nil {
		return ImportedEnvironment{}, err
	}
	name := uniqueName(plan.name, func(n string) bool {
		return slices.ContainsFunc(existing, func(e store.Environment) bool { return e.Name == n })
	})
	env, err := s.Store.ImportEnvironment(ctx, store.Environment{ID: store.NewID(), Name: name}, plan.providers, plan.teammates, plan.rules)
	if err != nil {
		return ImportedEnvironment{}, apiError(err)
	}
	return ImportedEnvironment{Environment: env, ToAdd: plan.toAdd, Templates: plan.templates}, nil
}

// checkEnvironmentFile validates every part of f and assigns the IDs of the records it makes.
// Its errors are 422s that name the offending value, never a host path.
func (s *Server) checkEnvironmentFile(f EnvironmentFile) (importPlan, error) {
	bad := func(format string, args ...any) error {
		return huma.Error422UnprocessableEntity(fmt.Sprintf(format, args...))
	}
	plan := importPlan{name: f.Name, toAdd: []ToAdd{}, templates: []string{}}
	if err := teammates.CheckName("environment", f.Name); err != nil {
		return plan, apiError(err)
	}
	switch {
	case len(f.Providers) > maxFileProviders, len(f.Teammates) > maxFileTeammates, len(f.Rules) > maxFileRules,
		len(f.Secrets) > maxFileSecrets, len(f.Templates) > maxFileTemplates:
		return plan, bad("the file lists more providers, teammates, rules, secrets or templates than an environment file may")
	}

	// Providers, by name. Teammates refer to them by name, rules and secrets to teammates.
	providers := map[string]store.Provider{}
	for _, fp := range f.Providers {
		if err := teammates.CheckName("provider", fp.Name); err != nil {
			return plan, apiError(err)
		}
		if _, dup := providers[fp.Name]; dup {
			return plan, bad("two providers are named %q", fp.Name)
		}
		k, ok := teammates.LookupKind(fp.Kind)
		if !ok {
			return plan, bad("provider %q has an unknown kind", fp.Name)
		}
		p := store.Provider{ID: store.NewID(), Name: fp.Name, Kind: fp.Kind}
		if fp.Kind == teammates.KindOpenAICompatible {
			var err error
			if p.BaseURL, _, err = teammates.BaseURL(fp.BaseURL); err != nil {
				return plan, apiError(err)
			}
			if fp.Model == "" {
				return plan, bad("provider %q needs a model", fp.Name)
			}
			if err := teammates.CheckModel(fp.Model); err != nil {
				return plan, apiError(err)
			}
			p.Model = fp.Model
		} else if fp.BaseURL != "" || fp.Model != "" {
			return plan, bad("only OpenAI-compatible providers have a base URL and model; %q does not", fp.Name)
		}
		providers[fp.Name] = p
		plan.providers = append(plan.providers, p)
		if k.APIKey {
			plan.toAdd = append(plan.toAdd, ToAdd{Kind: "providerKey", Name: fp.Name, ProviderID: p.ID})
		}
	}

	teammateIDs := map[string]string{}
	for _, ft := range f.Teammates {
		p := store.Teammate{
			ID: store.NewID(), Name: ft.Name, Role: strings.TrimSpace(ft.Role), Avatar: ft.Avatar,
			ApprovalScope: ft.ApprovalScope, Soul: ft.Soul, Harness: ft.Harness, Model: strings.TrimSpace(ft.Model),
			GitName: strings.TrimSpace(ft.GitName), GitEmail: strings.TrimSpace(ft.GitEmail),
		}
		if p.ApprovalScope == "" {
			p.ApprovalScope = teammates.ScopeEnvironment
		}
		if !slices.Contains([]string{teammates.ScopeSandbox, teammates.ScopeTeammate, teammates.ScopeEnvironment}, p.ApprovalScope) {
			return plan, bad("teammate %q has an unknown approval scope", p.Name)
		}
		if !slices.Contains([]string{teammates.HarnessOpenCode, teammates.HarnessClaude, teammates.HarnessCodex}, p.Harness) {
			return plan, bad("teammate %q has an unknown harness", p.Name)
		}
		if p.GitName == "" {
			p.GitName = p.Name
		}
		if p.GitEmail == "" {
			p.GitEmail = teammates.DefaultGitEmail(p.Name, p.ID)
		}
		for _, err := range []error{
			teammates.CheckName("teammate", p.Name), teammates.CheckRole(p.Role), teammates.CheckAvatar(p.Avatar),
			teammates.CheckSoul(p.Soul), teammates.CheckGitName(p.GitName), teammates.CheckGitEmail(p.GitEmail),
		} {
			if err != nil {
				return plan, apiError(err)
			}
		}
		if _, dup := teammateIDs[p.Name]; dup {
			return plan, bad("two teammates are named %q", p.Name)
		}
		if p.Model != "" {
			if err := teammates.CheckModel(p.Model); err != nil {
				return plan, apiError(err)
			}
		}
		if ft.Provider != "" {
			prov, ok := providers[ft.Provider]
			if !ok {
				return plan, bad("teammate %q uses provider %q, which the file does not list", p.Name, ft.Provider)
			}
			if !teammates.Supports(prov.Kind, p.Harness) {
				return plan, bad("provider %q can't run %s, which teammate %q uses", prov.Name, p.Harness, p.Name)
			}
			p.ProviderID = prov.ID
			if p.Model == "" && prov.Kind == teammates.KindOpenAICompatible {
				p.Model = prov.Model
			}
			if k, _ := teammates.LookupKind(prov.Kind); p.Model == "" && k.APIKey {
				return plan, bad("teammate %q needs a model for provider %q", p.Name, prov.Name)
			}
		}
		teammateIDs[p.Name] = p.ID
		plan.teammates = append(plan.teammates, p)
	}

	// Secrets are checked before the rules, which may refer to them. Their values are not in
	// the file: they are listed for the user to add.
	type scope struct{ teammate, name string }
	scopes := map[scope]bool{}
	var fileSecrets []store.Secret
	for _, fs := range f.Secrets {
		if err := secrets.CheckName(fs.Name); err != nil {
			return plan, apiError(err)
		}
		hosts, err := secrets.Hosts(fs.Hosts)
		if err != nil {
			return plan, apiError(err)
		}
		if fs.Teammate != "" {
			if _, ok := teammateIDs[fs.Teammate]; !ok {
				return plan, bad("secret %s is scoped to teammate %q, which the file does not list", fs.Name, fs.Teammate)
			}
		}
		if scopes[scope{fs.Teammate, fs.Name}] {
			return plan, bad("two secrets named %s in the same scope", fs.Name)
		}
		scopes[scope{fs.Teammate, fs.Name}] = true
		fileSecrets = append(fileSecrets, store.Secret{Name: fs.Name, Hosts: hosts})
		plan.toAdd = append(plan.toAdd, ToAdd{Kind: "secret", Name: fs.Name, Hosts: hosts, Teammate: fs.Teammate})
	}

	for _, fr := range f.Rules {
		host, err := policy.ValidPattern(fr.Host)
		if err != nil {
			return plan, apiError(err)
		}
		if len(fr.Ports) > 64 {
			return plan, bad("rule for %s lists more than 64 ports", fr.Host)
		}
		for _, port := range fr.Ports {
			if port < 1 || port > 65535 {
				return plan, bad("rule for %s has port %d; ports are 1 to 65535", fr.Host, port)
			}
		}
		if !slices.Contains([]string{store.ActionAllow, store.ActionProxy, store.ActionCaddy, store.ActionDeny}, fr.Action) {
			return plan, bad("rule for %s has an unknown action", fr.Host)
		}
		r := store.Rule{ID: store.NewID(), Host: host, Ports: fr.Ports, Action: fr.Action, Note: fr.Note}
		if fr.Teammate != "" {
			if r.TeammateID = teammateIDs[fr.Teammate]; r.TeammateID == "" {
				return plan, bad("rule for %s is scoped to teammate %q, which the file does not list", fr.Host, fr.Teammate)
			}
		}
		in := RuleConfigInput{Headers: fr.Headers, Caddyfile: fr.Caddyfile, Upstream: fr.Upstream, HostHeader: fr.HostHeader, SNI: fr.SNI, CA: fr.CA}
		if r.Config, err = s.checkRuleConfig(fr.Action, in, fileSecrets); err != nil {
			return plan, err
		}
		plan.rules = append(plan.rules, r)
	}

	for _, ft := range f.Templates {
		if _, err := templatespec.ParseYAML([]byte(ft.Spec)); err != nil {
			return plan, bad("a template spec of the file is invalid: %s", problemText(err))
		}
		plan.templates = append(plan.templates, ft.Spec)
	}
	return plan, nil
}

// problemText is the first problem of a template spec error, which names a line, not a host
// path, or the error itself.
func problemText(err error) string {
	if problems := templatespec.Problems(err); len(problems) > 0 {
		return problems[0].String()
	}
	return err.Error()
}

// uniqueName returns name, or name with the lowest number from 2 that taken does not report,
// shortening name so the result stays within the name limit.
func uniqueName(name string, taken func(string) bool) string {
	if !taken(name) {
		return name
	}
	runes := []rune(name)
	for n := 2; ; n++ {
		suffix := fmt.Sprintf(" %d", n)
		base := runes
		if limit := teammates.MaxNameLen - len(suffix); len(base) > limit {
			base = base[:limit]
		}
		if candidate := strings.TrimRight(string(base), " ") + suffix; !taken(candidate) {
			return candidate
		}
	}
}
