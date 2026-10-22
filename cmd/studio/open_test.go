package main

import (
	"errors"
	"slices"
	"testing"
)

func TestBrowserCommand(t *testing.T) {
	url := "http://127.0.0.1:7878/#studio-login=a&b"
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	for _, tt := range []struct {
		goos string
		env  map[string]string
		want []string
	}{
		{"windows", nil, []string{"rundll32", "url.dll,FileProtocolHandler", url}},
		{"darwin", nil, []string{"open", url}},
		{"linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, []string{"xdg-open", url}},
		{"linux", map[string]string{"DISPLAY": ":0"}, []string{"xdg-open", url}},
	} {
		cmd, err := browserCommand(tt.goos, url, env(tt.env))
		if err != nil {
			t.Fatalf("%s: %v", tt.goos, err)
		}
		if !slices.Equal(cmd.Args, tt.want) {
			t.Errorf("%s: args = %q, want %q", tt.goos, cmd.Args, tt.want)
		}
	}
	if _, err := browserCommand("linux", url, env(nil)); !errors.Is(err, errNoDisplay) {
		t.Errorf("linux without a display: %v, want errNoDisplay", err)
	}
}
