//go:build linux

// Command guestcopy runs in the spike's VM: it copies a tree with the guest's export and
// import, as a rebase's two VMs do, through a pipe within one process.
package main

import (
	"bytes"
	"io"
	"log"
	"os"

	"github.com/lukaskoebe/sandbox-studio/internal/guest"
)

func main() {
	src, dst := os.Args[1], os.Args[2]
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(guest.ExportWorkspaceAs(src, pw, &bytes.Buffer{}, guest.Zstd))
	}()
	if err := guest.ImportWorkspace(dst, pr, nil); err != nil {
		log.Fatal(err)
	}
}
