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
// into a tmpfs volume for near-zero I/O latency during model loading.
//
// Environment variables:
//   - FLASH_SRC: source directory (PVC mount, e.g., /src)
//   - FLASH_DST: destination directory (tmpfs mount, e.g., /models)
//   - FLASH_CONCURRENCY: number of parallel copy goroutines (default: 4)
//   - FLASH_P2P: enable peer-to-peer transfer (default: false)
//   - FLASH_P2P_PORT: P2P listen port (default: 9876)
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	src := os.Getenv("FLASH_SRC")
	dst := os.Getenv("FLASH_DST")
	concurrency := envInt("FLASH_CONCURRENCY", 4)

	if src == "" || dst == "" {
		log.Fatal("FLASH_SRC and FLASH_DST must be set")
	}

	log.Printf("flash-loader starting: src=%s dst=%s concurrency=%d", src, dst, concurrency)
	start := time.Now()

	// Discover files to copy
	var files []string
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
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
		dir := filepath.Dir(f)
		if dir != "." && !dirs[dir] {
			dirs[dir] = true
			dstDir := filepath.Join(dst, dir)
			if err := os.MkdirAll(dstDir, 0o755); err != nil {
				log.Fatalf("failed to create directory %s: %v", dstDir, err)
			}
		}
	}

	// Parallel copy with worker pool
	fileCh := make(chan string, len(files))
	for _, f := range files {
		fileCh <- f
	}
	close(fileCh)

	var (
		wg         sync.WaitGroup
		totalBytes atomic.Int64
		errCount   atomic.Int32
	)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for relPath := range fileCh {
				srcPath := filepath.Join(src, relPath)
				dstPath := filepath.Join(dst, relPath)

				n, err := copyFile(srcPath, dstPath)
				if err != nil {
					log.Printf("ERROR copying %s: %v", relPath, err)
					errCount.Add(1)
					continue
				}
				totalBytes.Add(n)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	totalMB := float64(totalBytes.Load()) / (1024 * 1024)
	rate := totalMB / elapsed.Seconds()

	log.Printf("flash-loader complete: %d files, %.1f MB, %.1f MB/s, %v elapsed",
		len(files), totalMB, rate, elapsed.Round(time.Millisecond))

	if errCount.Load() > 0 {
		log.Fatalf("flash-loader failed: %d copy errors", errCount.Load())
	}
}

func copyFile(src, dst string) (int64, error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = srcFile.Close() }()

	info, err := srcFile.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", src, err)
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", dst, err)
	}
	defer func() { _ = dstFile.Close() }()

	n, err := io.Copy(dstFile, srcFile)
	if err != nil {
		return n, fmt.Errorf("copy %s → %s: %w", src, dst, err)
	}

	return n, nil
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
