//go:build !linux

package main

import "os"

// fadviseSequential is a no-op on non-Linux platforms.
func fadviseSequential(_ *os.File, _ int64) {}
