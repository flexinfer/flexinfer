//go:build linux

package main

import (
	"os"
	"syscall"
)

// fadviseSequential hints the kernel to use sequential readahead for the file.
// This improves NVMe throughput by allowing the kernel to prefetch ahead of reads.
func fadviseSequential(f *os.File, size int64) {
	_ = syscall.Fadvise(int(f.Fd()), 0, size, syscall.FADV_SEQUENTIAL)
}
