package caddyrule

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// secretNameForm is the form of a name in {secret.NAME}.
var secretNameForm = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// FuzzCompile feeds Compile arbitrary Caddyfiles. A rule that compiles has no placeholder
// that reads Studio's environment or files, no secret reference left in its routes, and
// secrets bound to sorted, distinct upstream hosts.
func FuzzCompile(f *testing.F) {
	f.Add(gitReadOnly)
	for _, src := range []string{
		"respond 200",
		"reverse_proxy https://api.example.com {\n  header_up Authorization \"Bearer {secret.TOKEN}\"\n}",
		"reverse_proxy https://api.example.com:8443 {\n  header_up X-Key {secret.KEY}\n}",
		"reverse_proxy http://api.example.com {\n  header_up X-Key {secret.KEY}\n}",
		"reverse_proxy api.example.com {\n  header_up X-Key {file.x}\n}",
		"respond {env.HOME}",
		"respond {system.hostname}",
		"respond {secret.TOKEN}",
		"import other",
		"rewrite /a /b",
		"header X-A {http.request.host}",
		"{$HOME}",
		"reverse_proxy https://api.example.com {\n  transport http {\n    tls\n  }\n}",
		"@x path /a/*\nhandle @x {\n  respond \"ok\"\n}",
		"}",
		"respond \"unterminated",
	} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		c, err := Compile(src)
		if err != nil {
			return
		}
		if !json.Valid(c.Routes) {
			t.Fatalf("routes are not JSON: %s", c.Routes)
		}
		for _, bad := range []string{"{env.", "{file.", "{system.", "{secret."} {
			if strings.Contains(string(c.Routes), bad) {
				t.Errorf("compiled routes contain %q: %s", bad, c.Routes)
			}
		}
		for name, hosts := range c.Secrets {
			if !secretNameForm.MatchString(name) {
				t.Errorf("secret name %q", name)
			}
			if len(hosts) == 0 {
				t.Errorf("secret %s has no upstream host", name)
			}
			for i := 1; i < len(hosts); i++ {
				if hosts[i-1] >= hosts[i] {
					t.Errorf("secret %s hosts %q are not sorted and distinct", name, hosts)
				}
			}
		}
	})
}
