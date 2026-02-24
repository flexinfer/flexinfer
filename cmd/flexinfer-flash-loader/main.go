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
//   - FLASH_EXCLUDE: comma-separated directory/file prefixes to skip (default: .cache/,.git/)
//   - FLASH_VARIANT: when "fp16", skip fp32 safetensors when fp16 variant exists
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
	"syscall"
	"time"
)

// defaultExcludes are directory/file prefixes always skipped during copy.
// These are known to contain non-model data that wastes tmpfs space.
var defaultExcludes = []string{".cache/", ".git/"}

func main() {
	src := os.Getenv("FLASH_SRC")
	dst := os.Getenv("FLASH_DST")
	concurrency := envInt("FLASH_CONCURRENCY", 4)
	bufferKB := envInt("FLASH_BUFFER_KB", 4096)
	verify := envBool("FLASH_VERIFY", false)
	excludes := parseExcludes(os.Getenv("FLASH_EXCLUDE"))
	variant := strings.TrimSpace(os.Getenv("FLASH_VARIANT"))

	if src == "" || dst == "" {
		log.Fatal("FLASH_SRC and FLASH_DST must be set")
	}

	log.Printf("flash-loader starting: src=%s dst=%s concurrency=%d bufferKB=%d verify=%v excludes=%v variant=%q",
		src, dst, concurrency, bufferKB, verify, excludes, variant)
	start := time.Now()

	// Clean stale .flash-tmp files from previous interrupted runs
	cleanStaleTmpFiles(dst)

	// Discover files to copy, applying exclude filters and symlink skipping.
	// We use os.Lstat inside the walk callback because filepath.Walk follows
	// symlinks by default — the info passed to the callback is the target's
	// FileInfo, not the symlink itself. We need Lstat to detect symlinks.
	var files []fileEntry
	var excludedCount int
	var excludedBytes int64
	var symlinkCount int
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("WARN: walk error for %s: %v (skipping)", path, err)
			return nil // skip instead of failing
		}

		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}

		// Check for symlinks using Lstat (Walk follows symlinks transparently)
		if rel != "." {
			linfo, lerr := os.Lstat(path)
			if lerr == nil && linfo.Mode()&os.ModeSymlink != 0 {
				symlinkCount++
				if linfo.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Skip excluded directories entirely (prune subtree)
		if info.IsDir() {
			if rel != "." && shouldExclude(rel+"/", excludes) {
				excludedCount++
				return filepath.SkipDir
			}
			return nil
		}

		// Skip excluded files
		if shouldExclude(rel, excludes) {
			excludedCount++
			excludedBytes += info.Size()
			return nil
		}

		files = append(files, fileEntry{rel: rel, size: info.Size()})
		return nil
	})
	if err != nil {
		log.Fatalf("failed to walk source directory: %v", err)
	}

	// Apply variant-aware filtering (skip fp32 when fp16 exists)
	if variant == "fp16" {
		before := len(files)
		files = filterFP32Variants(files)
		dropped := before - len(files)
		if dropped > 0 {
			log.Printf("variant=fp16: skipped %d fp32 files (fp16 variant exists)", dropped)
		}
	}

	if len(files) == 0 {
		log.Printf("no files found in %s (excluded=%d symlinks=%d), nothing to copy", src, excludedCount, symlinkCount)
		return
	}

	// Calculate total source size
	var totalBytes int64
	for _, f := range files {
		totalBytes += f.size
	}
	totalMB := float64(totalBytes) / (1024 * 1024)

	log.Printf("discovered %d files to copy (%.1f MB), excluded=%d (%.1f MB) symlinks=%d",
		len(files), totalMB, excludedCount, float64(excludedBytes)/(1024*1024), symlinkCount)

	// Pre-flight space check: verify tmpfs has enough room before copying
	if err := checkAvailableSpace(dst, totalBytes); err != nil {
		log.Fatalf("pre-flight space check failed: %v", err)
	}

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

	// Post-copy verification (respects same exclude filters)
	if verify {
		log.Printf("verifying destination integrity...")
		if err := verifyIntegrity(src, dst, excludes, variant); err != nil {
			log.Fatalf("flash-loader verification failed: %v", err)
		}
		log.Printf("verification passed")
	}
}

type fileEntry struct {
	rel  string
	size int64
}

// parseExcludes splits a comma-separated exclude string and merges with defaults.
func parseExcludes(raw string) []string {
	result := append([]string{}, defaultExcludes...)
	if raw == "" {
		return result
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// shouldExclude returns true if the relative path matches any exclude prefix.
func shouldExclude(rel string, excludes []string) bool {
	for _, prefix := range excludes {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
		// Also match if any path component starts with the prefix
		if idx := strings.LastIndex(rel, "/"); idx >= 0 {
			if strings.HasPrefix(rel[idx+1:], prefix) {
				return true
			}
		}
	}
	return false
}

// filterFP32Variants removes fp32 safetensors files when a corresponding fp16
// variant exists. HuggingFace repos often contain both model.safetensors and
// model.fp16.safetensors; when using fp16 variant, the fp32 files waste space.
func filterFP32Variants(files []fileEntry) []fileEntry {
	// Build set of all fp16 safetensors files
	fp16Set := make(map[string]bool)
	for _, f := range files {
		if strings.HasSuffix(f.rel, ".fp16.safetensors") {
			// Map "model.fp16.safetensors" → "model.safetensors"
			base := strings.TrimSuffix(f.rel, ".fp16.safetensors") + ".safetensors"
			fp16Set[base] = true
		}
	}

	if len(fp16Set) == 0 {
		return files
	}

	// Filter out fp32 files that have fp16 counterparts
	filtered := make([]fileEntry, 0, len(files))
	for _, f := range files {
		if strings.HasSuffix(f.rel, ".safetensors") && !strings.HasSuffix(f.rel, ".fp16.safetensors") {
			if fp16Set[f.rel] {
				log.Printf("  variant filter: skipping fp32 %s (fp16 variant exists)", f.rel)
				continue
			}
		}
		filtered = append(filtered, f)
	}
	return filtered
}

// checkAvailableSpace verifies that the destination filesystem has enough space
// for the files to be copied. Returns an error with a clear message if space
// is insufficient, preventing the "no space left on device" crash-loop.
func checkAvailableSpace(dst string, requiredBytes int64) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dst, &stat); err != nil {
		log.Printf("WARN: cannot check destination space: %v (proceeding anyway)", err)
		return nil
	}
	availableBytes := int64(stat.Bavail) * int64(stat.Bsize)
	requiredMB := float64(requiredBytes) / (1024 * 1024)
	availableMB := float64(availableBytes) / (1024 * 1024)

	// Require 5% headroom to avoid running into the limit during copy
	headroom := int64(float64(requiredBytes) * 0.05)
	if headroom < 10*1024*1024 {
		headroom = 10 * 1024 * 1024 // minimum 10 MB headroom
	}

	if requiredBytes+headroom > availableBytes {
		return fmt.Errorf(
			"insufficient tmpfs space: need %.1f MB (+%.0f MB headroom) but only %.1f MB available. "+
				"Increase spec.flashLoader.tmpfsSizeLimit or clean source data",
			requiredMB, float64(headroom)/(1024*1024), availableMB)
	}

	log.Printf("space check: need %.1f MB, available %.1f MB (%.0f%% utilization after copy)",
		requiredMB, availableMB, (requiredMB/availableMB)*100)
	return nil
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

// verifyIntegrity walks the source directory and checks that every non-excluded
// file exists in the destination with matching size. Respects the same exclude
// and variant filters used during copy.
func verifyIntegrity(src, dst string, excludes []string, variant string) error {
	// Build set of expected files using the same filtering logic
	var expected []fileEntry
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors during verification walk
		}

		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}

		// Skip symlinks
		if rel != "." {
			linfo, lerr := os.Lstat(path)
			if lerr == nil && linfo.Mode()&os.ModeSymlink != 0 {
				if linfo.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		if info.IsDir() {
			if rel != "." && shouldExclude(rel+"/", excludes) {
				return filepath.SkipDir
			}
			return nil
		}

		if shouldExclude(rel, excludes) {
			return nil
		}

		expected = append(expected, fileEntry{rel: rel, size: info.Size()})
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk source for verification: %w", err)
	}

	// Apply variant filtering
	if variant == "fp16" {
		expected = filterFP32Variants(expected)
	}

	var mismatches []string
	for _, f := range expected {
		dstPath := filepath.Join(dst, f.rel)
		dstInfo, err := os.Stat(dstPath)
		if err != nil {
			mismatches = append(mismatches, fmt.Sprintf("%s: missing in destination", f.rel))
			continue
		}
		if f.size != dstInfo.Size() {
			mismatches = append(mismatches, fmt.Sprintf("%s: size mismatch (src=%d dst=%d)", f.rel, f.size, dstInfo.Size()))
		}
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
