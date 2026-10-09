package caddyrule

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
)

// gitReadOnly is v1's example rule, with its secret written the v2 way.
const gitReadOnly = `# Read-only Git over HTTPS: clone and fetch, but no push.
@discover {
  method GET HEAD
  path_regexp gitrefs ^/[^/]+/[^/]+/info/refs$
  expression ` + "`{http.request.uri.query} == 'service=git-upload-pack'`" + `
}
@fetch {
  method POST
  path_regexp gitfetch ^/[^/]+/[^/]+/git-upload-pack$
  expression ` + "`{http.request.uri.query} == ''`" + `
}
handle @discover {
  reverse_proxy https://git.example.com {
    header_up Host git.example.com
    header_up Authorization "Basic {secret.GIT_READ_BASIC}"
    header_up -Cookie
    header_up -Proxy-Authorization
  }
}
handle @fetch {
  reverse_proxy https://git.example.com {
    header_up Host git.example.com
    header_up Authorization "Basic {secret.GIT_READ_BASIC}"
    header_up -Cookie
    header_up -Proxy-Authorization
  }
}
handle {
  respond "Read only" 403
}
`

func TestCompileGitReadOnly(t *testing.T) {
	c, err := Compile(gitReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][]string{"GIT_READ_BASIC": {"git.example.com"}}; !maps.EqualFunc(c.Secrets, want, slices.Equal) {
		t.Errorf("secrets %v, want %v", c.Secrets, want)
	}
	routes := string(c.Routes)
	if strings.Contains(routes, "{secret.") || !strings.Contains(routes, marker("GIT_READ_BASIC")) {
		t.Errorf("secret references aren't markers: %s", routes)
	}
	if strings.Count(routes, `"protocol":"studio"`) != 2 || strings.Contains(routes, `"protocol":"http"`) {
		t.Errorf("reverse_proxy doesn't use Studio's transport: %s", routes)
	}
	var parsed []any
	if err := json.Unmarshal(c.Routes, &parsed); err != nil {
		t.Fatal(err)
	}
}

func TestCompileAccepts(t *testing.T) {
	for name, src := range map[string]string{
		"respond":        `respond "hello" 200`,
		"quoted brace":   `respond "}" 200`,
		"plain upstream": "reverse_proxy api.example.com:8080",
		"handle_path":    "handle_path /v1/* {\n  reverse_proxy https://api.example.com\n}",
		"headers":        "header X-Studio yes\nheader -Server",
		"rewrite":        "rewrite /old /new\nuri strip_prefix /api",
		"redir":          "redir https://example.com{uri}",
		"not matcher":    "@w not method GET\nrespond @w 405",
		"host matcher":   "@h host api.example.com\nrespond @h 204",
		"header_down":    "reverse_proxy https://api.example.com {\n  header_down -Set-Cookie\n}",
		"handle_response": "reverse_proxy https://api.example.com {\n  @err status 5xx\n" +
			"  handle_response @err {\n    respond \"upstream failed\" 502\n  }\n}",
		"secret in added header": "reverse_proxy https://api.example.com {\n  header_up +X-Key {secret.KEY}\n}",
		"two upstreams":          "reverse_proxy https://a.example.com https://b.example.com {\n  header_up X-Key {secret.KEY}\n}",
		"safe retry matcher": "reverse_proxy https://api.example.com {\n" +
			"  lb_retry_match {\n    method GET\n    expression `{http.request.method} == 'GET'`\n  }\n}",
		"safe nested not retry matcher": "reverse_proxy https://api.example.com {\n" +
			"  lb_retry_match {\n    not {\n      expression `{http.request.method} == 'GET'`\n    }\n  }\n}",
		"timeouts": "reverse_proxy https://api.example.com {\n  transport http {\n    tls\n" +
			"    dial_timeout 5s\n    response_header_timeout 30s\n  }\n}",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Compile(src); err != nil {
				t.Errorf("%v", err)
			}
		})
	}
}

func TestCompileSecretHosts(t *testing.T) {
	c, err := Compile("handle /a {\n  reverse_proxy https://a.example.com {\n    header_up X-Key {secret.KEY}\n  }\n}\n" +
		"handle {\n  reverse_proxy https://b.example.com:8443 https://A.example.com {\n" +
		"    header_up X-Key \"{secret.KEY} {secret.OTHER}\"\n  }\n}")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"KEY": {"a.example.com", "b.example.com"}, "OTHER": {"a.example.com", "b.example.com"}}
	if !maps.EqualFunc(c.Secrets, want, slices.Equal) {
		t.Errorf("secrets %v, want %v", c.Secrets, want)
	}
}

func TestCompileRefuses(t *testing.T) {
	for _, tc := range []struct{ name, src, err string }{
		{"empty", " \n ", "empty"},
		{"too large", strings.Repeat("#", MaxSize+1), "larger than"},
		{"NUL", "respond \"a\x00\"", "NUL"},
		{"env variable", `respond {$HOME}`, "{$NAME}"},
		{"import", "import /etc/caddy/snippet", "import"},
		{"import after newline", "respond ok\nimport foo", "line 2: import"},
		{"quoted import", `"import" /etc/caddy/snippet`, "import"},
		{"backquoted import", "`import` /etc/caddy/snippet", "import"},
		{"import after a block", "handle {\n  respond ok\n} import foo", "import"},
		{"import as an argument", `respond "import" 200`, "import"},
		{"missing brace", "handle {\n  respond ok\n", "line 2: missing }"},
		{"extra brace", "handle {\n  respond ok\n}\n}", "line 4: unexpected }"},
		{"unknown directive", "file_server", "unrecognized directive"},
		{"global options", "}\n{\n  debug\n}\nhttp:// {", "line 1: unexpected }"},
		{"second site", "}\n:8080 {\n  respond ok\n}\nhttp:// {", "line 1: unexpected }"},
		{"handle_errors", "handle_errors {\n  respond oops\n}", "handle_errors"},
		{"env placeholder", "respond {env.HOME}", "{env.*}"},
		{"file placeholder", "respond {file./etc/passwd}", "{file.*}"},
		{"system placeholder", "header X-Wd {system.wd}", "{system.*}"},
		{"env placeholder in a header name", "header {env.HOME} x", "{env.*}"},
		{"env placeholder in an expression", "@e expression `{env.HOME} == 'x'`\nrespond @e 200", "{env.*}"},
		{"ph", "@e expression `ph(req, 'env.' + 'HOME') == 'x'`\nrespond @e 200", "ph()"},
		{"ph with a comment", "@e expression `ph // x\n(req, 'env.HOME') == 'x'`\nrespond @e 200", "ph()"},
		{"ph in retry matcher", "reverse_proxy https://api.example.com {\n  lb_retry_match {\n    expression `ph(req, 'env.HOME') == 'x'`\n  }\n}", "ph()"},
		{"ph in nested not retry matcher", "reverse_proxy https://api.example.com {\n  lb_retry_match {\n    not {\n      expression `ph(req, 'env.HOME') == 'x'`\n    }\n  }\n}", "ph()"},
		{"secret in respond", "respond {secret.KEY}", "only be sent upstream"},
		{"secret in a matcher", "@e expression `{secret.KEY}.startsWith('a')`\nrespond @e 200", "only be sent upstream"},
		{"secret in a response header", "header X-Key {secret.KEY}", "only be sent upstream"},
		{"secret in header_down", "reverse_proxy https://api.example.com {\n  header_down X-Key {secret.KEY}\n}", "only be sent upstream"},
		{"secret in a header name", "reverse_proxy https://api.example.com {\n  header_up {secret.KEY} x\n}", "only be sent upstream"},
		{"secret in replace", "reverse_proxy https://api.example.com {\n  header_up X-Key a {secret.KEY}\n}", "only be sent upstream"},
		{"secret in rewrite", "rewrite * /x?key={secret.KEY}", "only be sent upstream"},
		{"secret over plain HTTP", "reverse_proxy api.example.com:80 {\n  header_up X-Key {secret.KEY}\n}", "HTTPS"},
		{"malformed secret", "reverse_proxy https://api.example.com {\n  header_up X-Key {secret.MY-KEY}\n}", "{secret.NAME}"},
		{"placeholder upstream", "reverse_proxy {http.request.header.X-Upstream}", "fixed"},
		{"dynamic upstreams", "reverse_proxy {\n  dynamic srv _api._tcp.example.com\n}", "dynamic_upstreams"},
		{"no upstream", "reverse_proxy", "upstream"},
		{"unix upstream", "reverse_proxy unix//run/docker.sock", "fixed"},
		{"health checks", "reverse_proxy https://api.example.com {\n  health_uri /health\n}", "health_checks"},
		{"insecure TLS", "reverse_proxy https://api.example.com {\n  transport http {\n    tls_insecure_skip_verify\n  }\n}", "insecure_skip_verify"},
		{"TLS server name", "reverse_proxy https://api.example.com {\n  transport http {\n    tls_server_name evil.example.com\n  }\n}", "server_name"},
		{"client certificate", "reverse_proxy https://api.example.com {\n  transport http {\n    tls_client_auth /etc/cert.pem /etc/key.pem\n  }\n}", "client_certificate"},
		{"forward proxy", "reverse_proxy https://api.example.com {\n  transport http {\n    network_proxy url http://10.0.0.1:3128\n  }\n}", "network_proxy"},
		{"HTTP/3", "reverse_proxy https://api.example.com {\n  transport http {\n    versions 3\n  }\n}", "versions"},
		{"resolver", "reverse_proxy https://api.example.com {\n  transport http {\n    resolvers 10.0.0.1\n  }\n}", "resolver"},
		{"fastcgi", "reverse_proxy localhost:9000 {\n  transport fastcgi\n}", "transport"},
		{"trusted proxies", "reverse_proxy https://api.example.com {\n  trusted_proxies private_ranges\n}", "trusted_proxies"},
		{"file matcher", "@f file /etc/passwd\nrespond @f 200", "file"},
		{"remote_ip matcher", "@r remote_ip 10.0.0.0/8\nrespond @r 200", "remote_ip"},
		{"vars", "vars x y", "vars"},
		{"encode", "encode gzip", "encode"},
		{"templates", "templates", "unrecognized directive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile(tc.src)
			if err == nil {
				t.Fatalf("compiled")
			}
			if !strings.Contains(err.Error(), tc.err) {
				t.Errorf("error %q, want it to mention %q", err, tc.err)
			}
		})
	}
}

func TestCompileErrorLines(t *testing.T) {
	_, err := Compile("respond ok\n\nreverse_proxy {\n  bogus_option x\n}")
	if err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Errorf("error %v, want it at line 4", err)
	}
}
