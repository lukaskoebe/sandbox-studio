// Command studio-agent runs inside every sandbox and connects back to Studio.
package main

import (
	"fmt"
	"os"

	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version.Version)
		return
	}
	fmt.Fprintln(os.Stderr, "studio-agent: not implemented yet")
	os.Exit(2)
}
