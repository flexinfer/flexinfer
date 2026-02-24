//go:build linux

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// fadviseSequential hints the kernel to use sequential readahead for the file.
// This improves NVMe throughput by allowing the kernel to prefetch ahead of reads.
func fadviseSequential(f *os.File, size int64) {
	_ = unix.Fadvise(int(f.Fd()), 0, size, unix.FADV_SEQUENTIAL)
}
