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

var workspaceTime = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC).Unix()

// browse answers a KindFiles request from workspace, as the guest agent does from disk.
func browse(req agentproto.FilesRequest) (agentproto.FilesReply, error) {
	name := path.Clean("/" + req.Path)[1:]
	switch req.Op {
	case agentproto.FilesList:
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
	case agentproto.FilesRead:
		data, ok := workspace[name]
		if !ok {
			return agentproto.FilesReply{}, errors.New("no such file or directory")
		}
		return agentproto.FilesReply{Size: int64(len(data)), MimeType: "text/plain; charset=utf-8", Data: []byte(data)}, nil
	}
	return agentproto.FilesReply{}, errors.New("unknown files op")
}
