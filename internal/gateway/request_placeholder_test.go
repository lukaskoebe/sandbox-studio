package gateway

import (
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
)

// A request carries a placeholder to an unbound host when it holds the placeholder of a secret
// that doesn't cover the host, in its headers, path, query or Basic credentials. Placeholders of
// bound secrets, and of none, don't count.
func TestCarriesPlaceholder(t *testing.T) {
	all := []secrets.Binding{
		{Name: "API", Placeholder: "studio-api", Hosts: []string{"api.example.com"}},
		{Name: "OTHER", Placeholder: "studio-other", Hosts: []string{"other.example.net"}},
		{Name: "FOREIGN", Placeholder: "studio-foreign", Foreign: true},
	}
	request := func(path, query string, header map[string]string) *http.Request {
		r, err := http.NewRequest(http.MethodGet, "https://api.example.com/"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.URL.RawQuery = query
		for k, v := range header {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		name string
		r    *http.Request
		host string
		want bool
	}{
		{"nothing to swap", request("v1/items", "", nil), "api.example.com", false},
		{"the bound secret", request("v1/items", "", map[string]string{"Authorization": "Bearer studio-api"}), "api.example.com", false},
		{"an unbound secret in a header", request("v1/items", "", map[string]string{"X-Token": "studio-other"}), "api.example.com", true},
		{"an unbound secret in the path", request("studio-other/items", "", nil), "api.example.com", true},
		{"an unbound secret in the query", request("v1/items", "key=studio-other", nil), "api.example.com", true},
		{"a foreign secret", request("v1/items", "key=studio-foreign", nil), "api.example.com", true},
		{"basic credentials", request("v1/items", "", map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("me:studio-other"))}), "api.example.com", true},
		{"the bound secret on its own host", request("v1/items", "", map[string]string{"X-Token": "studio-api"}), "other.example.net", true},
	}
	for _, c := range cases {
		if got := carriesPlaceholder(c.r, all, c.host); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
