// Package teammates holds the fixed tables and validation rules of teammates and providers:
// which provider kinds exist, which harnesses each supports, and what a teammate's fields
// may contain. The store keeps the records; the API validates with this package.
package teammates

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
)

// ErrInvalid is wrapped by errors describing a teammate or provider field that is refused.
var ErrInvalid = errors.New("invalid")

// Harnesses.
const (
	HarnessOpenCode = "opencode"
	HarnessClaude   = "claude"
	HarnessCodex    = "codex"
)

// Provider kinds.
const (
	KindAnthropicAPI        = "anthropic_api"
	KindOpenAIAPI           = "openai_api"
	KindOpenAICompatible    = "openai_compatible"
	KindClaudeSubscription  = "claude_subscription"
	KindChatGPTSubscription = "chatgpt_subscription"
)

// Provider states. An API-key provider is key_required until its key is added.
const (
	StateReady         = "ready"
	StateLoginRequired = "login_required"
	StateKeyRequired   = "key_required"
)

// Scopes of new rules from approvals of a teammate's sandboxes: the sandbox, the teammate's
// sandboxes or the environment. ScopeEnvironment is the default and the behaviour before the
// setting existed.
const (
	ScopeSandbox     = "sandbox"
	ScopeTeammate    = "teammate"
	ScopeEnvironment = "environment"
)

// Kind describes a provider kind. The table is fixed (docs/teammates.md).
type Kind struct {
	Kind      string
	Harnesses []string
	// APIKey kinds keep a key in the vault, bound to Host (or to the base URL's host for
	// openai_compatible). Other kinds are subscription logins.
	APIKey bool
	Host   string
	// EnvVar is the variable the harness reads its credential from; the guest gets the
	// placeholder of the provider's secret in it.
	EnvVar string
}

var kinds = []Kind{
	{Kind: KindAnthropicAPI, Harnesses: []string{HarnessClaude, HarnessOpenCode}, APIKey: true, Host: "api.anthropic.com", EnvVar: "ANTHROPIC_API_KEY"},
	{Kind: KindOpenAIAPI, Harnesses: []string{HarnessCodex, HarnessOpenCode}, APIKey: true, Host: "api.openai.com", EnvVar: "OPENAI_API_KEY"},
	{Kind: KindOpenAICompatible, Harnesses: []string{HarnessOpenCode, HarnessCodex}, APIKey: true, EnvVar: "OPENAI_API_KEY"},
	{Kind: KindClaudeSubscription, Harnesses: []string{HarnessClaude}, EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"},
	{Kind: KindChatGPTSubscription, Harnesses: []string{HarnessCodex}},
}

// LookupKind returns the description of a provider kind.
func LookupKind(kind string) (Kind, bool) {
	i := slices.IndexFunc(kinds, func(k Kind) bool { return k.Kind == kind })
	if i < 0 {
		return Kind{}, false
	}
	return kinds[i], true
}

// Supports reports whether a provider of kind can run harness.
func Supports(kind, harness string) bool {
	k, ok := LookupKind(kind)
	return ok && slices.Contains(k.Harnesses, harness)
}

// Limits of teammate and provider fields.
const (
	MaxNameLen     = 64
	MaxRoleLen     = 200
	MaxSoulBytes   = 16 << 10
	MaxModelLen    = 128
	MaxGitNameLen  = 100
	MaxGitEmailLen = 254
	MaxBaseURLLen  = 2048
)

// CheckName validates the name of a teammate or provider: one line of printable text.
func CheckName(what, name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("%w: the %s name must not be empty or start or end with spaces", ErrInvalid, what)
	}
	if utf8.RuneCountInString(name) > MaxNameLen {
		return fmt.Errorf("%w: the %s name is longer than %d characters", ErrInvalid, what, MaxNameLen)
	}
	return checkLine(what+" name", name)
}

// CheckRole validates a teammate's short role description.
func CheckRole(role string) error {
	if utf8.RuneCountInString(role) > MaxRoleLen {
		return fmt.Errorf("%w: the role is longer than %d characters", ErrInvalid, MaxRoleLen)
	}
	return checkLine("role", role)
}

// CheckSoul validates a teammate's soul: markdown of bounded size without NUL bytes.
func CheckSoul(soul string) error {
	switch {
	case len(soul) > MaxSoulBytes:
		return fmt.Errorf("%w: the soul is larger than %d KiB", ErrInvalid, MaxSoulBytes>>10)
	case !utf8.ValidString(soul):
		return fmt.Errorf("%w: the soul is not valid UTF-8", ErrInvalid)
	case strings.IndexByte(soul, 0) >= 0:
		return fmt.Errorf("%w: the soul contains a NUL byte", ErrInvalid)
	}
	return nil
}

// modelPattern covers model IDs such as claude-sonnet-4-5, gpt-5.1-codex,
// openrouter/anthropic/claude or llama3.1:8b. Models end up in harness config files, so
// quotes, spaces and control characters are refused.
var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]*$`)

// CheckModel validates a model ID. An empty model is checked by the caller.
func CheckModel(model string) error {
	if len(model) > MaxModelLen || !modelPattern.MatchString(model) {
		return fmt.Errorf("%w: model %q must be at most %d letters, digits and . _ : / @ + -", ErrInvalid, model, MaxModelLen)
	}
	return nil
}

// CheckGitName validates a git author name. Git refuses < > and newlines in identities,
// and Studio writes it into git config, so control characters are refused as well.
func CheckGitName(name string) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name {
		return fmt.Errorf("%w: the git name must not be empty or start or end with spaces", ErrInvalid)
	}
	if utf8.RuneCountInString(name) > MaxGitNameLen {
		return fmt.Errorf("%w: the git name is longer than %d characters", ErrInvalid, MaxGitNameLen)
	}
	if strings.ContainsAny(name, `<>"\`) {
		return fmt.Errorf("%w: the git name must not contain < > \" or \\", ErrInvalid)
	}
	return checkLine("git name", name)
}

// CheckGitEmail validates a git author email: local@domain with one @, ASCII, no spaces,
// quotes or angle brackets.
func CheckGitEmail(email string) error {
	at := strings.IndexByte(email, '@')
	if len(email) > MaxGitEmailLen || at <= 0 || at == len(email)-1 || strings.Count(email, "@") != 1 {
		return fmt.Errorf("%w: git email %q must look like name@example.com and be at most %d characters", ErrInvalid, email, MaxGitEmailLen)
	}
	for _, c := range email {
		if c <= ' ' || c > '~' || strings.ContainsRune(`<>"\(),;:[]`, c) {
			return fmt.Errorf("%w: git email %q contains %q", ErrInvalid, email, c)
		}
	}
	if !validDomain(email[at+1:]) {
		return fmt.Errorf("%w: git email %q has an invalid domain", ErrInvalid, email)
	}
	return nil
}

func validDomain(d string) bool {
	_, err := policy.ValidPattern(d)
	return err == nil && !strings.HasPrefix(d, "*") && strings.Contains(d, ".")
}

// checkLine refuses control characters, including newlines, and invalid UTF-8.
func checkLine(what, s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: the %s is not valid UTF-8", ErrInvalid, what)
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return fmt.Errorf("%w: the %s must be one line without control characters", ErrInvalid, what)
		}
	}
	return nil
}

// Avatars. A teammate's avatar is one emoji, one of the generated avatars, or empty to show
// its initials. Generated avatars are fixed shapes the web UI draws in the teammate's color,
// so nothing is uploaded.
const avatarShapePrefix = "shape:"

// AvatarShapes are the generated avatars, stored as "shape:<name>".
var AvatarShapes = []string{"circle", "square", "diamond", "triangle", "pentagon", "hexagon", "star", "cross"}

// MaxAvatarRunes bounds an emoji sequence: the longest family emoji is 7 code points.
const MaxAvatarRunes = 16

// CheckAvatar validates a teammate's avatar: empty, a generated avatar or a single emoji.
func CheckAvatar(avatar string) error {
	if avatar == "" {
		return nil
	}
	if shape, ok := strings.CutPrefix(avatar, avatarShapePrefix); ok {
		if !slices.Contains(AvatarShapes, shape) {
			return fmt.Errorf("%w: %q is not a generated avatar", ErrInvalid, avatar)
		}
		return nil
	}
	if !isEmoji(avatar) {
		return fmt.Errorf("%w: the avatar must be one emoji or a generated avatar (shape:<name>)", ErrInvalid)
	}
	return nil
}

// isEmoji reports whether s is one emoji: a pictograph with its variation selector and skin
// tone, pictographs joined by zero-width joiners, a flag (two regional indicators) or a
// keycap. It recognises the forms emoji pickers produce; it is not a grapheme segmentation.
func isEmoji(s string) bool {
	rs := []rune(s)
	switch {
	case len(rs) == 0 || len(rs) > MaxAvatarRunes:
		return false
	case len(rs) == 2 && isRegional(rs[0]) && isRegional(rs[1]):
		return true
	case strings.ContainsRune("0123456789#*", rs[0]) && rs[len(rs)-1] == 0x20E3:
		return len(rs) == 2 || len(rs) == 3 && rs[1] == 0xFE0F
	}
	for i := 0; ; {
		if i >= len(rs) || !isPictograph(rs[i]) {
			return false
		}
		i++
		if i < len(rs) && rs[i] == 0xFE0F {
			i++
		}
		if i < len(rs) && isSkinTone(rs[i]) {
			i++
		}
		if i == len(rs) {
			return true
		}
		if rs[i] != 0x200D {
			return false
		}
		i++
	}
}

func isPictograph(r rune) bool {
	switch {
	case isSkinTone(r), isRegional(r):
		return false
	case r == 0xA9, r == 0xAE, r == 0x203C, r == 0x2049, r == 0x2122, r == 0x2139, r == 0x24C2,
		r == 0x25B6, r == 0x25C0, r == 0x2328, r == 0x23CF, r == 0x2B50, r == 0x2B55, r == 0x3030,
		r == 0x303D, r == 0x3297, r == 0x3299:
		return true
	case r >= 0x2194 && r <= 0x2199, r >= 0x21A9 && r <= 0x21AA, r >= 0x231A && r <= 0x231B,
		r >= 0x23E9 && r <= 0x23F3, r >= 0x23F8 && r <= 0x23FA, r >= 0x25AA && r <= 0x25AB,
		r >= 0x25FB && r <= 0x25FE, r >= 0x2600 && r <= 0x27BF, r >= 0x2934 && r <= 0x2935,
		r >= 0x2B05 && r <= 0x2B07, r >= 0x2B1B && r <= 0x2B1C, r >= 0x1F000 && r <= 0x1FAFF:
		return true
	}
	return false
}

func isSkinTone(r rune) bool { return r >= 0x1F3FB && r <= 0x1F3FF }

func isRegional(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// Slug is name lower-cased with runs of other characters turned into single dashes, e.g.
// "Ada Lovelace" → "ada-lovelace". It is empty if name has no ASCII letters or digits.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, c := range strings.ToLower(name) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(c)
			dash = false
		} else {
			dash = true
		}
		if b.Len() >= 48 {
			break
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// DefaultGitEmail is <slug>@agents.invalid; the .invalid TLD never resolves, so commits
// never point at someone's real address. fallback is used when name has no slug.
func DefaultGitEmail(name, fallback string) string {
	slug := Slug(name)
	if slug == "" {
		slug = fallback
	}
	return slug + "@agents.invalid"
}

// SecretName is the vault name of a provider's key: PROVIDER_<NAME>_KEY, e.g.
// PROVIDER_WORK_ANTHROPIC_KEY. fallback is used when name has no slug.
func SecretName(name, fallback string) string {
	slug := Slug(name)
	if slug == "" {
		slug = fallback
	}
	s := strings.ToUpper(strings.ReplaceAll(slug, "-", "_"))
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "_")
	}
	return "PROVIDER_" + s + "_KEY"
}

// BaseURL validates the base URL of an OpenAI-compatible provider and returns it
// normalized, with the host its key is bound to. The URL must be HTTPS and name a public
// host; the gateway refuses private, loopback and link-local upstreams regardless.
func BaseURL(raw string) (string, string, error) {
	if len(raw) > MaxBaseURLLen {
		return "", "", fmt.Errorf("%w: the base URL is longer than %d characters", ErrInvalid, MaxBaseURLLen)
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Opaque != "" {
		return "", "", fmt.Errorf("%w: the base URL must be an https:// URL", ErrInvalid)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", "", fmt.Errorf("%w: the base URL must not contain credentials, a query or a fragment", ErrInvalid)
	}
	host, err := policy.ValidPattern(u.Hostname())
	if err != nil || strings.HasPrefix(host, "*") {
		return "", "", fmt.Errorf("%w: the base URL has an invalid host", ErrInvalid)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsGlobalUnicast() || ip.IsPrivate() {
			return "", "", fmt.Errorf("%w: the base URL must name a public host", ErrInvalid)
		}
	} else if host == "localhost" || strings.HasSuffix(host, ".localhost") || !strings.Contains(host, ".") {
		return "", "", fmt.Errorf("%w: the base URL must name a public host", ErrInvalid)
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), host, nil
}
