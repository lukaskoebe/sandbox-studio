//go:build e2e

package e2e

import (
	"errors"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// workspace is every e2e sandbox's /workspace as the file browser sees it: a few files in
// the languages Files highlights.
var workspace = map[string]string{
	"README.md": "# Webshop\n\nA *small* shop with coupons.\n",
	"main.go":   "package main\n\nimport \"fmt\"\n\n// main greets.\nfunc main() {\n\tfmt.Println(\"hello\", 3)\n}\n",
	"web/package.json": `{
  "name": "web",
  "private": true
}
`,
	"web/src/checkout/coupon.ts": `import type { Cart } from "./cart"

// A code applies once per cart, before shipping.
export function applyCoupon(cart: Cart, percent: number) {
  if (cart.subtotal < 20) return cart
  return { ...cart, discount: (cart.subtotal * percent) / 100 }
}
`,
	"web/src/checkout/checkout.tsx": "export function Checkout() {\n  return <form className=\"checkout\" />\n}\n",
}

// gitRepo is /workspace as git sees it in every e2e sandbox, and gitDiffs its changes' diffs.
var gitRepo = agentproto.GitRepo{
	Branch: "fix-coupon", Head: "3f2a9c1d8e7b6a5f4e3d2c1b0a9f8e7d6c5b4a39", Upstream: "origin/main", Ahead: 2,
	Changes: []agentproto.GitChange{
		{Path: "web/src/checkout/coupon.ts", Area: agentproto.GitStaged, Status: "modified", Added: 2, Removed: 1},
		{Path: "web/src/checkout/coupon.test.ts", Area: agentproto.GitStaged, Status: "added", Added: 3},
		{Path: "main.go", Area: agentproto.GitUnstaged, Status: "modified", Added: 1, Removed: 1},
		{Path: "docs/old.md", Area: agentproto.GitUnstaged, Status: "deleted", Removed: 2},
		{Path: "notes.txt", Area: agentproto.GitUntracked, Status: "added"},
	},
}

var gitDiffs = map[string]string{
	"staged web/src/checkout/coupon.ts": `diff --git a/web/src/checkout/coupon.ts b/web/src/checkout/coupon.ts
--- a/web/src/checkout/coupon.ts
+++ b/web/src/checkout/coupon.ts
@@ -3,5 +3,6 @@ import type { Cart } from "./cart"
 // A code applies once per cart, before shipping.
 export function applyCoupon(cart: Cart, percent: number) {
-  if (cart.subtotal < 20) return cart
+  if (cart.subtotal < 20 || percent <= 0) return cart
+  if (cart.discount) return cart
   return { ...cart, discount: (cart.subtotal * percent) / 100 }
 }
`,
	"staged web/src/checkout/coupon.test.ts": `diff --git a/web/src/checkout/coupon.test.ts b/web/src/checkout/coupon.test.ts
new file mode 100644
--- /dev/null
+++ b/web/src/checkout/coupon.test.ts
@@ -0,0 +1,3 @@
+import { applyCoupon } from "./coupon"
+
+test("once per cart", () => {})
`,
	"unstaged main.go": `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -5,4 +5,4 @@ import "fmt"
 // main greets.
 func main() {
-	fmt.Println("hello", 3)
+	fmt.Println("hello, shop", 3)
 }
`,
	"unstaged docs/old.md": `diff --git a/docs/old.md b/docs/old.md
deleted file mode 100644
--- a/docs/old.md
+++ /dev/null
@@ -1,2 +0,0 @@
-# Old
-Gone.
`,
	"untracked notes.txt": `diff --git a/notes.txt b/notes.txt
new file mode 100644
--- /dev/null
+++ b/notes.txt
@@ -0,0 +1 @@
+Ask about free shipping.
`,
}

// tmp is every e2e sandbox's /tmp.
var tmp = map[string]string{
	"build.log":            "vite v6 building for production...\n✓ 42 modules transformed.\n",
	"vite-cache/deps.json": "{}\n",
}

// computer is every e2e sandbox's whole file system: /workspace, /tmp and a few more.
func computer() map[string]string {
	all := map[string]string{
		"etc/hostname":       "shop\n",
		"etc/os-release":     "PRETTY_NAME=\"Ubuntu 24.04 LTS\"\n",
		"home/agent/.bashrc": "export EDITOR=vi\n",
	}
	for p, data := range workspace {
		all["workspace/"+p] = data
	}
	for p, data := range tmp {
		all["tmp/"+p] = data
	}
	return all
}

var workspaceTime = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC).Unix()

// browse answers a KindFiles request from workspace, as the guest agent does from disk.
func browse(req agentproto.FilesRequest) (agentproto.FilesReply, error) {
	reply, err := browseIn(req)
	reply.Root = req.Root
	return reply, err
}

func browseIn(req agentproto.FilesRequest) (agentproto.FilesReply, error) {
	name := path.Clean("/" + req.Path)[1:]
	workspace := workspace
	switch req.Root {
	case agentproto.FilesRootTmp:
		workspace = tmp
	case agentproto.FilesRootFS:
		workspace = computer()
	}
	switch req.Op {
	case agentproto.FilesList:
		if _, ok := workspace[name]; ok {
			return agentproto.FilesReply{}, errors.New("not a directory")
		}
		prefix := ""
		if name != "" {
			prefix = name + "/"
		}
		seen := map[string]bool{}
		reply := agentproto.FilesReply{Entries: []agentproto.FileEntry{}}
		for p, data := range workspace {
			rest, ok := strings.CutPrefix(p, prefix)
			if !ok {
				continue
			}
			child, _, dir := strings.Cut(rest, "/")
			if seen[child] {
				continue
			}
			seen[child] = true
			e := agentproto.FileEntry{Name: child, Type: "file", Size: int64(len(data)), ModTime: workspaceTime}
			if dir {
				e.Type, e.Size = "dir", 0
			}
			reply.Entries = append(reply.Entries, e)
		}
		if len(reply.Entries) == 0 && name != "" {
			return agentproto.FilesReply{}, errors.New("no such file or directory")
		}
		slices.SortFunc(reply.Entries, func(a, b agentproto.FileEntry) int {
			if (a.Type == "dir") != (b.Type == "dir") {
				if a.Type == "dir" {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Name, b.Name)
		})
		return reply, nil
	case agentproto.FilesIndex:
		reply := agentproto.FilesReply{Changed: []string{"web/src/checkout/coupon.ts"}}
		for p := range workspace {
			reply.Paths = append(reply.Paths, p)
		}
		slices.Sort(reply.Paths)
		return reply, nil
	case agentproto.FilesGit:
		return agentproto.FilesReply{Repos: []agentproto.GitRepo{gitRepo}}, nil
	case agentproto.FilesGitDiff:
		for _, c := range gitRepo.Changes {
			if c.Path == req.File && c.Area == req.Area {
				return agentproto.FilesReply{Diff: gitDiffs[c.Area+" "+c.Path]}, nil
			}
		}
		return agentproto.FilesReply{}, errors.New("no such change")
	case agentproto.FilesRead:
		data, ok := workspace[name]
		if !ok {
			return agentproto.FilesReply{}, errors.New("no such file or directory")
		}
		return agentproto.FilesReply{Size: int64(len(data)), MimeType: "text/plain; charset=utf-8", Data: []byte(data)}, nil
	}
	return agentproto.FilesReply{}, errors.New("unknown files op")
}
