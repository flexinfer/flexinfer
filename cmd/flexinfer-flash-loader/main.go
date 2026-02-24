/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// flash-loader is an init container that parallel-copies model files from a PVC
// or hostPath into a tmpfs volume for near-zero I/O latency during model loading.
//
// Environment variables:
//   - FLASH_SRC: source directory (PVC mount or hostPath, e.g., /src)
//   - FLASH_DST: destination directory (tmpfs mount, e.g., /models)
//   - FLASH_CONCURRENCY: number of parallel copy goroutines (default: 4)
//   - FLASH_BUFFER_KB: per-worker I/O buffer size in KB (default: 4096)
//   - FLASH_VERIFY: enable post-copy size verification (default: false)
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	src := os.Getenv("FLASH_SRC")
	dst := os.Getenv("FLASH_DST")
	concurrency := envInt("FLASH_CONCURRENCY", 4)
	bufferKB := envInt("FLASH_BUFFER_KB", 4096)
	verify := envBool("FLASH_VERIFY", false)

	if src == "" || dst == "" {
		log.Fatal("FLASH_SRC and FLASH_DST must be set")
	}

	log.Printf("flash-loader starting: src=%s dst=%s concurrency=%d bufferKB=%d verify=%v",
		src, dst, concurrency, bufferKB, verify)
	start := time.Now()

	// Clean stale .flash-tmp files from previous interrupted runs
	cleanStaleTmpFiles(dst)

	// Discover files to copy
	var files []fileEntry
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			files = append(files, fileEntry{rel: rel, size: info.Size()})
		}
		return nil
	})
	if err != nil {
		log.Fatalf("failed to walk source directory: %v", err)
	}

	if len(files) == 0 {
		log.Printf("no files found in %s, nothing to copy", src)
		return
	}

	log.Printf("discovered %d files to copy", len(files))

	// Create directory structure first
	dirs := make(map[string]bool)
	for _, f := range files {
		dir := filepath.Dir(f.rel)
		if dir != "." && !dirs[dir] {
			dirs[dir] = true
			dstDir := filepath.Join(dst, dir)
			if err := os.MkdirAll(dstDir, 0o755); err != nil {
				log.Fatalf("failed to create directory %s: %v", dstDir, err)
			}
		}
	}

	// Parallel copy with worker pool
	fileCh := make(chan fileEntry, len(files))
	for _, f := range files {
		fileCh <- f
	}
	close(fileCh)

	bufferSize := bufferKB * 1024

	var (
		wg           sync.WaitGroup
		copiedBytes  atomic.Int64
		copiedCount  atomic.Int32
		skippedBytes atomic.Int64
		skippedCount atomic.Int32
		errCount     atomic.Int32
	)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, bufferSize)
			for entry := range fileCh {
				srcPath := filepath.Join(src, entry.rel)
				dstPath := filepath.Join(dst, entry.rel)

				if !shouldCopy(srcPath, dstPath) {
					skippedBytes.Add(entry.size)
					skippedCount.Add(1)
					continue
				}

				n, err := copyFileAtomic(srcPath, dstPath, buf)
				if err != nil {
					log.Printf("ERROR copying %s: %v", entry.rel, err)
					errCount.Add(1)
					continue
				}
				copiedBytes.Add(n)
				copiedCount.Add(1)

				if entry.size > 100*1024*1024 {
					elapsed := time.Since(start)
					mb := float64(n) / (1024 * 1024)
					log.Printf("  copied %s (%.1f MB) [%v elapsed]", entry.rel, mb, elapsed.Round(time.Millisecond))
				}
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	copiedMB := float64(copiedBytes.Load()) / (1024 * 1024)
	skippedMB := float64(skippedBytes.Load()) / (1024 * 1024)
	rate := float64(0)
	if elapsed.Seconds() > 0 {
		rate = copiedMB / elapsed.Seconds()
	}

	log.Printf("flash-loader complete: copied=%d (%.1f MB, %.1f MB/s) skipped=%d (%.1f MB) elapsed=%v",
		copiedCount.Load(), copiedMB, rate,
		skippedCount.Load(), skippedMB,
		elapsed.Round(time.Millisecond))

	if errCount.Load() > 0 {
		log.Fatalf("flash-loader failed: %d copy errors", errCount.Load())
	}

	// Post-copy verification
	if verify {
		log.Printf("verifying destination integrity...")
		if err := verifyIntegrity(src, dst); err != nil {
			log.Fatalf("flash-loader verification failed: %v", err)
		}
		log.Printf("verification passed")
	}
}

type fileEntry struct {
	rel  string
	size int64
}

// shouldCopy returns true if the file needs to be copied (incremental copy).
// Compares file sizes; skips if destination exists with matching size.
func shouldCopy(src, dst string) bool {
	dstInfo, err := os.Stat(dst)
	if err != nil {
		return true // destination doesn't exist
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		return true // can't stat source, try copying anyway
	}
	return srcInfo.Size() != dstInfo.Size()
}

// copyFileAtomic copies src to dst using a temporary file and atomic rename.
// Writes to <dst>.flash-tmp, then renames on success.
func copyFileAtomic(src, dst string, buf []byte) (int64, error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = srcFile.Close() }()

	info, err := srcFile.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", src, err)
	}

	// Hint the kernel for sequential read
	fadviseSequential(srcFile, info.Size())

	tmpPath := dst + ".flash-tmp"
	dstFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", tmpPath, err)
	}

	n, copyErr := io.CopyBuffer(dstFile, srcFile, buf)
	if closeErr := dstFile.Close(); closeErr != nil && copyErr == nil {
		copyErr = closeErr
	}

	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return n, fmt.Errorf("copy %s → %s: %w", src, dst, copyErr)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return n, fmt.Errorf("rename %s → %s: %w", tmpPath, dst, err)
	}

	return n, nil
}

// cleanStaleTmpFiles removes leftover .flash-tmp files from interrupted runs.
func cleanStaleTmpFiles(dir string) {
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if !info.IsDir() && strings.HasSuffix(path, ".flash-tmp") {
			log.Printf("cleaning stale tmp file: %s", path)
			_ = os.Remove(path)
		}
		return nil
	})
}

// verifyIntegrity walks the source directory and checks that every file exists
// in the destination with matching size.
func verifyIntegrity(src, dst string) error {
	var mismatches []string
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, rel)
		dstInfo, err := os.Stat(dstPath)
		if err != nil {
			mismatches = append(mismatches, fmt.Sprintf("%s: missing in destination", rel))
			return nil
		}
		if info.Size() != dstInfo.Size() {
			mismatches = append(mismatches, fmt.Sprintf("%s: size mismatch (src=%d dst=%d)", rel, info.Size(), dstInfo.Size()))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk source for verification: %w", err)
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("%d file(s) failed verification:\n  %s", len(mismatches), strings.Join(mismatches, "\n  "))
	}
	return nil
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	default:
		return def
	}
}
