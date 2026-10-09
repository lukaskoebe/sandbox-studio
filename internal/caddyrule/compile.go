// Package caddyrule runs the Caddy rules of the network policy. A Caddy rule's config is the
// body of a Caddyfile site block (matchers, handle, reverse_proxy, respond, …). Studio
// compiles it with Caddy's own Caddyfile adapter, checks the result against an allowlist,
// and serves the requests of the rule's intercepted connections with an embedded Caddy.
//
// The checks keep a rule to rewriting, answering and proxying requests: no files, no
// environment, no extra listeners, and upstreams that are fixed host names, dialed like
// every other connection the gateway makes. Secrets are referenced as {secret.NAME} and
// may only be sent upstream in reverse_proxy request headers, over TLS, to hosts they are
// bound to (the caller checks the bindings, from Compiled.Secrets).
package caddyrule

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	_ "github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile" // the Caddyfile adapter
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/headers"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/rewrite"
)

// MaxSize is the largest Caddyfile a rule may have, in bytes.
const MaxSize = 32 << 10

// Compiled is a rule's Caddyfile turned into Caddy routes.
type Compiled struct {
	Routes json.RawMessage
	// Secrets maps the name of each secret the rule references to the upstream hosts it is
	// sent to.
	Secrets map[string][]string
}

var (
	secretRef = regexp.MustCompile(`\{secret\.([A-Za-z0-9_]+)\}`)
	// Caddy's global placeholders read Studio's environment and files. Caddy resolves them
	// before any of ours, so they can only be kept out of the config.
	globalPlaceholder = regexp.MustCompile(`\{(env|file|system)\.`)
	// CEL expressions can call the placeholder function with a computed name.
	celPlaceholderFunc = regexp.MustCompile(`\bph\b`)
	adapterLine        = regexp.MustCompile(`Caddyfile:(\d+)`)
)

// Compile checks a rule's Caddyfile and compiles it. Errors are meant for the user and
// give line numbers within src.
func Compile(src string) (Compiled, error) {
	if strings.TrimSpace(src) == "" {
		return Compiled{}, errors.New("the Caddyfile is empty")
	}
	if len(src) > MaxSize {
		return Compiled{}, fmt.Errorf("the Caddyfile is larger than %d KiB", MaxSize>>10)
	}
	if strings.ContainsRune(src, 0) {
		return Compiled{}, errors.New("the Caddyfile contains a NUL byte")
	}
	// {$NAME} is replaced from Studio's environment while parsing.
	if strings.Contains(src, "{$") {
		return Compiled{}, errors.New("{$NAME} environment variables are not available; reference secrets as {secret.NAME}")
	}
	tokens, err := caddyfile.Tokenize([]byte(src), "Caddyfile")
	if err != nil {
		return Compiled{}, userError(err, 0)
	}
	// The rule stays inside the site block it's wrapped in.
	depth, last := 0, 0
	for _, t := range tokens {
		last = t.Line
		switch {
		// Caddy's parser imports on the token's text, quoted or not, so even a quoted import
		// would read files on the host.
		case t.Text == "import":
			return Compiled{}, fmt.Errorf("line %d: import is not available", t.Line)
		case t.Quoted():
		case t.Text == "{":
			depth++
		case t.Text == "}":
			if depth--; depth < 0 {
				return Compiled{}, fmt.Errorf("line %d: unexpected }", t.Line)
			}
		}
	}
	if depth > 0 {
		return Compiled{}, fmt.Errorf("line %d: missing }", last)
	}

	// The rule is the body of a site block, which is how the adapter reads it, one line down.
	site := "http:// {\n" + src + "\n}\n"
	out, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(site), map[string]any{"filename": "Caddyfile"})
	if err != nil {
		return Compiled{}, userError(err, 1)
	}
	routes, err := siteRoutes(out)
	if err != nil {
		return Compiled{}, err
	}

	// The walk replaces the secret references it accepts with markers. Any reference left is
	// in a place that would show the secret to the sandbox or send it somewhere unchecked.
	c := &compiler{secrets: map[string][]string{}}
	if err := c.routes(routes); err != nil {
		return Compiled{}, err
	}
	if err := walkStrings(routes, checkString); err != nil {
		return Compiled{}, err
	}
	for name, hosts := range c.secrets {
		slices.Sort(hosts)
		c.secrets[name] = slices.Compact(hosts)
	}
	raw, err := json.Marshal(routes)
	if err != nil {
		return Compiled{}, err
	}
	return Compiled{Routes: raw, Secrets: c.secrets}, nil
}

// userError rewrites the adapter's positions to lines of the rule.
func userError(err error, offset int) error {
	msg := adapterLine.ReplaceAllStringFunc(err.Error(), func(m string) string {
		n, _ := strconv.Atoi(strings.TrimPrefix(m, "Caddyfile:"))
		return fmt.Sprintf("line %d", n-offset)
	})
	return errors.New(msg)
}

// siteRoutes takes the routes out of the adapted config, which must hold nothing else.
func siteRoutes(adapted []byte) ([]any, error) {
	dec := json.NewDecoder(bytes.NewReader(adapted))
	dec.UseNumber()
	var cfg map[string]any
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	notRoutes := errors.New("the Caddyfile can only contain routes: no global options, other sites or server settings")
	apps, _ := cfg["apps"].(map[string]any)
	if len(cfg) != 1 || len(apps) != 1 {
		return nil, notRoutes
	}
	httpApp, _ := apps["http"].(map[string]any)
	servers, _ := httpApp["servers"].(map[string]any)
	if len(httpApp) != 1 || len(servers) != 1 {
		return nil, notRoutes
	}
	for _, s := range servers {
		srv, _ := s.(map[string]any)
		for k := range srv {
			if k != "listen" && k != "routes" {
				if k == "errors" {
					return nil, errors.New("handle_errors is not available")
				}
				return nil, notRoutes
			}
		}
		routes, _ := srv["routes"].([]any)
		return routes, nil
	}
	return nil, notRoutes
}

// What a rule may use. Anything else is refused, including modules this package doesn't
// import, which the adapter already reports as unknown directives.
var (
	// No encode: masking secrets in responses needs bodies Studio can read.
	allowedHandlers = []string{
		"subroute", "reverse_proxy", "static_response", "headers", "rewrite", "error",
		"copy_response", "copy_response_headers",
	}
	allowedMatchers = []string{"method", "path", "path_regexp", "query", "header", "header_regexp", "expression", "not", "protocol", "host"}
	allowedProxy    = []string{
		"handler", "upstreams", "headers", "transport", "rewrite", "handle_response", "load_balancing",
		"flush_interval", "request_buffers", "response_buffers", "stream_timeout", "stream_close_delay",
	}
	allowedTransport = []string{
		"protocol", "tls", "keep_alive", "max_conns_per_host", "dial_timeout",
		"dial_fallback_delay", "response_header_timeout", "expect_continue_timeout",
		"max_response_header_size", "read_timeout", "write_timeout",
	}
	allowedStaticResponse = []string{"handler", "status_code", "headers", "body", "close", "abort"}
)

type compiler struct {
	secrets map[string][]string
}

func (c *compiler) routes(v any) error {
	list, ok := v.([]any)
	if !ok && v != nil {
		return errors.New("unexpected routes in the adapted config")
	}
	for _, r := range list {
		route, ok := r.(map[string]any)
		if !ok {
			return errors.New("unexpected route in the adapted config")
		}
		for k, v := range route {
			var err error
			switch k {
			case "group", "terminal":
			case "match":
				err = c.matcherSets(v)
			case "handle":
				err = c.handlers(v)
			default:
				err = fmt.Errorf("route option %s is not available", k)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *compiler) matcherSets(v any) error {
	sets, ok := v.([]any)
	if !ok {
		return errors.New("unexpected matchers in the adapted config")
	}
	for _, s := range sets {
		set, ok := s.(map[string]any)
		if !ok {
			return errors.New("unexpected matcher in the adapted config")
		}
		for name, m := range set {
			if !slices.Contains(allowedMatchers, name) {
				return fmt.Errorf("the %s matcher is not available", name)
			}
			switch name {
			case "not":
				if err := c.matcherSets(m); err != nil {
					return err
				}
			case "expression":
				err := walkStrings(m, func(s string) error {
					if celPlaceholderFunc.MatchString(s) {
						return errors.New("expressions can't use ph(); write placeholders as {name}")
					}
					return nil
				})
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (c *compiler) handlers(v any) error {
	list, ok := v.([]any)
	if !ok {
		return errors.New("unexpected handlers in the adapted config")
	}
	for _, item := range list {
		h, ok := item.(map[string]any)
		if !ok {
			return errors.New("unexpected handler in the adapted config")
		}
		name, _ := h["handler"].(string)
		if !slices.Contains(allowedHandlers, name) {
			return fmt.Errorf("the %s handler is not available", name)
		}
		var err error
		switch name {
		case "subroute":
			if _, ok := h["errors"]; ok {
				return errors.New("handle_errors is not available")
			}
			err = c.routes(h["routes"])
		case "reverse_proxy":
			err = c.reverseProxy(h)
		case "static_response":
			err = onlyKeys(h, allowedStaticResponse, "respond")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *compiler) reverseProxy(h map[string]any) error {
	if err := onlyKeys(h, allowedProxy, "reverse_proxy"); err != nil {
		return err
	}
	if lb, ok := h["load_balancing"].(map[string]any); ok {
		if retryMatch, ok := lb["retry_match"]; ok {
			if err := c.matcherSets(retryMatch); err != nil {
				return err
			}
		}
	}
	ups, _ := h["upstreams"].([]any)
	if len(ups) == 0 {
		return errors.New("reverse_proxy needs an upstream, such as reverse_proxy https://api.example.com")
	}
	var hosts []string
	for _, u := range ups {
		up, _ := u.(map[string]any)
		dial, _ := up["dial"].(string)
		if len(up) != 1 || dial == "" {
			return errors.New("reverse_proxy upstreams must be fixed addresses, such as https://api.example.com")
		}
		host, err := upstreamHost(dial)
		if err != nil {
			return err
		}
		hosts = append(hosts, host)
	}

	transport := map[string]any{}
	if t, ok := h["transport"]; ok {
		transport, _ = t.(map[string]any)
		if transport == nil || transport["protocol"] != "http" {
			return errors.New("reverse_proxy only has the http transport")
		}
		if err := onlyKeys(transport, allowedTransport, "the reverse_proxy transport"); err != nil {
			return err
		}
	}
	_, tls := transport["tls"]
	if tls {
		cfg, _ := transport["tls"].(map[string]any)
		for k := range cfg {
			return fmt.Errorf("reverse_proxy TLS option %s is not available: upstreams are verified against the system's roots, by their host name", k)
		}
	}
	// Studio's transport dials like the gateway and fills in the secrets.
	transport["protocol"] = transportProtocol
	h["transport"] = transport

	// Secrets go in the values of the request headers set for the upstream. Caddy only gets
	// markers, which the transport replaces as the request leaves.
	refs := 0
	headers, _ := h["headers"].(map[string]any)
	request, _ := headers["request"].(map[string]any)
	for _, op := range []string{"set", "add"} {
		fields, _ := request[op].(map[string]any)
		for _, values := range fields {
			list, _ := values.([]any)
			for i, v := range list {
				s, ok := v.(string)
				if !ok {
					continue
				}
				list[i] = secretRef.ReplaceAllStringFunc(s, func(ref string) string {
					name := secretRef.FindStringSubmatch(ref)[1]
					refs++
					c.secrets[name] = append(c.secrets[name], hosts...)
					return marker(name)
				})
			}
		}
	}
	if refs > 0 && !tls {
		return errors.New("secrets are only sent over HTTPS: use an https:// upstream")
	}

	if hr, ok := h["handle_response"].([]any); ok {
		for _, item := range hr {
			resp, _ := item.(map[string]any)
			if err := c.routes(resp["routes"]); err != nil {
				return err
			}
		}
	}
	return nil
}

// upstreamHost checks a reverse_proxy dial address and returns its host.
func upstreamHost(dial string) (string, error) {
	bad := fmt.Errorf("reverse_proxy upstream %q must be a fixed host name and port", dial)
	if strings.ContainsAny(dial, "{}/\\ \t\r\n") {
		return "", bad
	}
	host, port, err := net.SplitHostPort(dial)
	if err != nil || host == "" {
		return "", bad
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", bad
	}
	return strings.ToLower(strings.TrimSuffix(host, ".")), nil
}

func onlyKeys(m map[string]any, allowed []string, what string) error {
	for k := range m {
		if !slices.Contains(allowed, k) {
			return fmt.Errorf("%s option %s is not available", what, k)
		}
	}
	return nil
}

// checkString refuses the placeholders a rule can't have.
func checkString(s string) error {
	if m := globalPlaceholder.FindStringSubmatch(s); m != nil {
		if m[1] == "env" {
			return errors.New("{env.*} placeholders are not available; reference secrets as {secret.NAME}")
		}
		return fmt.Errorf("{%s.*} placeholders are not available", m[1])
	}
	if strings.Contains(s, "{secret.") {
		if strings.Count(s, "{secret.") != len(secretRef.FindAllString(s, -1)) {
			return errors.New("secret references look like {secret.NAME}, with letters, digits and _ in the name")
		}
		return errors.New("secrets can only be sent upstream in request headers: use header_up inside reverse_proxy, with an https:// upstream")
	}
	return nil
}

// walkStrings calls f with every string in v, map keys included, and stops at its first
// error.
func walkStrings(v any, f func(string) error) error {
	switch v := v.(type) {
	case string:
		return f(v)
	case []any:
		for _, item := range v {
			if err := walkStrings(item, f); err != nil {
				return err
			}
		}
	case map[string]any:
		for k, item := range v {
			if err := f(k); err != nil {
				return err
			}
			if err := walkStrings(item, f); err != nil {
				return err
			}
		}
	}
	return nil
}
