package main

import "io/fs"

// diskUsage is the file's size: Windows' allocated size takes a call per file.
func diskUsage(info fs.FileInfo) int64 { return info.Size() }
