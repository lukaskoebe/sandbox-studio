package reports

import "testing"

func TestAnonymize(t *testing.T) {
	names := []Name{{"checkout-service", "computer"}, {"Maximiliane", "teammate"}, {"acme/webshop", "repository"}, {"webshop", "repository"}, {"Al", "teammate"}, {"computer", "computer"}}
	for _, c := range []struct{ in, want string }{
		{"Maximiliane in checkout-service saw it", "<teammate> in <computer> saw it"},
		{"checkout-service checkout-service", "<computer> <computer>"},
		{"the computer computer", "the <computer> <computer>"},
		{"ask al and Al", "ask al and Al"},
		{"cloned https://github.com/acme/webshop.git into /workspace/webshop", "cloned https://github.com/<path> into /workspace/<project>"},
		{"fetch https://user:pw@git.company.local/x/y?token=1 failed.", "fetch https://<host>/<path> failed."},
		{"push to https://git.studio.internal/fj/acme/webshop.git", "push to https://git.studio.internal/<forge>/<repository>"},
		{"npm GET https://registry.npmjs.org/vite/-/vite-6.3.5.tgz 403", "npm GET https://registry.npmjs.org/vite/-/vite-6.3.5.tgz 403"},
		{"git@gitlab.example.org:team/app.git and git@github.com:acme/webshop.git", "git@<host>:<repository> and git@github.com:<repository>"},
		{"mail lukas.k@example.com", "mail <email>"},
		{"pushed to git.corp.internal, nas.local and Printer.LAN", "pushed to <host>, <host> and <host>"},
		{"see .env.local, settings.local.json and api.studio.internal", "see .env.local, settings.local.json and api.studio.internal"},
		{"10.0.0.12 and 192.168.178.0/24 but 127.0.0.1 and 0.0.0.0", "<ip> and <ip> but 127.0.0.1 and 0.0.0.0"},
		{"fe80::1 and 2001:db8::8a2e:370:7334 at 12:30:45 and ::1", "<ip> and <ip> at 12:30:45 and ::1"},
		{"/home/lukas/.config and /home/agent/x and C:\\Users\\Lukas\\AppData", "/home/<user>/.config and /home/agent/x and C:\\Users\\<user>\\AppData"},
		{"key sk-ant-api03-abcdefghijklmnopqrstuv and ghp_abcdefghijklmnopqrstuvwxyz0123", "key <secret> and <secret>"},
		{"commit 3f2a9c1e5b7d4a6f8e0c2b4d6f8a0c2e4b6d8f0a ok", "commit 3f2a9c1e5b7d4a6f8e0c2b4d6f8a0c2e4b6d8f0a ok"},
		{"token AbCdEfGhIjKlMnOpQrStUvWxYz0123456789", "token <secret>"},
		{"see http://localhost:5173/app and http://localhost:3000.", "see http://localhost:5173/app and http://localhost:3000."},
	} {
		got, _ := Anonymize(c.in, names)
		if got != c.want {
			t.Errorf("Anonymize(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
	if _, n := Anonymize("Maximiliane and Maximiliane in checkout-service", names); n != 3 {
		t.Errorf("count %d", n)
	}
}
