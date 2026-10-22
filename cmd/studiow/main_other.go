//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "studiow is Studio's Windows tray launcher. Run studio instead.")
	os.Exit(2)
}
