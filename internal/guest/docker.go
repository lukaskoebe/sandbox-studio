//go:build linux

package guest

import "io"

// ExportDocker writes Docker's data, the Docker disk mounted at root, as an archive to w for
// a disk resize. Unlike a workspace archive it keeps devices, FIFOs and extended
// attributes: image layers hold overlay whiteouts, opaque directories and file
// capabilities. Docker and containerd must not run, as in transfer mode.
func ExportDocker(root string, w io.Writer, warn io.Writer, c Compression) error {
	return exportTree(root, w, warn, treeOptions{special: true}, c)
}

// ImportDocker extracts an ExportDocker archive into the empty Docker disk at root, with
// ImportWorkspace's checks.
func ImportDocker(root string, r io.Reader) error {
	return importTree(root, r, true)
}
