// Command gen writes the manifest objects of the Windows executables; see package winres.
// It runs from internal/winres, as go generate does.
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/lukaskoebe/sandbox-studio/internal/winres"
)

func main() {
	for _, o := range winres.Outputs {
		b, err := winres.Object(o.Arch, winres.Manifest(o.Name))
		if err != nil {
			log.Fatal(err)
		}
		path := filepath.Join("..", "..", filepath.FromSlash(o.Path))
		if err := os.WriteFile(path, b, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
