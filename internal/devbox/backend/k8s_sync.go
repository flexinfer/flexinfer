package backend

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// defaultSyncExcludes are patterns excluded from tar-pipe workspace sync.
// These match the exclude patterns from scripts/devbox-sync.sh.
var defaultSyncExcludes = []string{
	".git",
	"node_modules",
	"vendor",
	".build",
	"__pycache__",
	".cache",
	".venv",
	".DS_Store",
	".pyc",
	"bin",
	".loom",
	".worktrees",
	".swiftpm",
	"xcuserdata",
	"dist",
	".sandbox-policy.json",
	// Go/lint build caches that some projects keep project-local.
	".gocache",
	".go",
	".gotmp",
	".golangci-lint-cache",
	// Temporary/generated directories.
	".tmp",
	"tmp",
}

// MaxSyncBytes is the default maximum uncompressed tar size (200 MB).
const MaxSyncBytes int64 = 200 * 1024 * 1024

// Deprecated: SyncWorkspace uses the legacy tar-pipe sync mode, which streams
// local directories into a pod via SPDY exec. This mode is being replaced by
// the sandbox.Controller unified interface with WebSocket exec support.
// Gate behind DEVBOX_SYNC_MODE=tar-pipe to use; will be removed in a future version.
//
// SyncWorkspace streams local directories into a running pod via exec.
// It creates a tar.gz archive spanning all dirs (each placed at its RemotePath)
// and pipes it into `tar xzf - -C /` inside the pod.
func (k *K8sBackend) SyncWorkspace(ctx context.Context, podName string, dirs []SyncDir, extraExcludes []string, maxBytes int64) error {
	if len(dirs) == 0 {
		return nil
	}
	if maxBytes <= 0 {
		maxBytes = MaxSyncBytes
	}

	excludes := make(map[string]bool, len(defaultSyncExcludes)+len(extraExcludes))
	for _, e := range defaultSyncExcludes {
		excludes[e] = true
	}
	for _, e := range extraExcludes {
		excludes[e] = true
	}

	// Create tar.gz in memory.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	var totalBytes int64
	for _, d := range dirs {
		if err := addDirToTar(tw, d.LocalPath, d.RemotePath, excludes, &totalBytes, maxBytes); err != nil {
			tw.Close()
			gw.Close()
			return fmt.Errorf("tar %s: %w", d.LocalPath, err)
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}

	// Pipe into pod via SPDY exec.
	return k.pipeTarIntoPod(ctx, podName, buf.Bytes())
}

// pipeTarIntoPod streams a tar.gz payload into a pod and extracts it.
func (k *K8sBackend) pipeTarIntoPod(ctx context.Context, podName string, payload []byte) error {
	req := k.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(k.namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "devbox",
			Command:   []string{"tar", "xzf", "-", "-C", "/"},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	// Tar-pipe sync always uses SPDY because stdin piping over WebSocket
	// requires bidirectional stream support that differs from exec semantics.
	executor, err := remotecommand.NewSPDYExecutor(k.restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("create sync executor: %w", err)
	}

	var stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  bytes.NewReader(payload),
		Stderr: &stderr,
	}); err != nil {
		return fmt.Errorf("sync workspace: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	return nil
}

// addDirToTar walks localDir and adds files to the tar writer with paths
// rooted at remotePath. Skips excluded patterns and enforces a max size.
func addDirToTar(tw *tar.Writer, localDir, remotePath string, excludes map[string]bool, totalBytes *int64, maxBytes int64) error {
	return filepath.WalkDir(localDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}

		// Check if this entry matches an exclude pattern.
		base := d.Name()
		if excludes[base] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary files by extension.
		if !d.IsDir() && isBinaryExcluded(base) {
			return nil
		}

		// Compute the path inside the tar.
		rel, err := filepath.Rel(localDir, path)
		if err != nil {
			return nil
		}
		// Strip the leading "./" from remotePath join.
		tarPath := filepath.Join(remotePath, rel)
		// Ensure forward slashes for tar.
		tarPath = filepath.ToSlash(tarPath)
		// Strip leading "/" so tar paths are relative (extracted with -C /).
		tarPath = strings.TrimPrefix(tarPath, "/")

		info, err := d.Info()
		if err != nil {
			return nil
		}

		// Skip symlinks, devices, etc.
		if !info.Mode().IsRegular() && !info.IsDir() {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil
		}
		header.Name = tarPath

		if info.IsDir() {
			header.Name += "/"
			return tw.WriteHeader(header)
		}

		// Enforce max size.
		*totalBytes += info.Size()
		if *totalBytes > maxBytes {
			return fmt.Errorf("sync payload exceeds %d MB limit", maxBytes/(1024*1024))
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return nil // skip unreadable files
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})
}

// isBinaryExcluded returns true for file extensions that indicate compiled
// binaries or object files that should be excluded from workspace sync.
func isBinaryExcluded(name string) bool {
	// Skip Go binaries: files with no extension in the root that are large
	// are caught by the size limit. Here we catch common patterns.
	ext := filepath.Ext(name)
	switch ext {
	case ".pyc", ".pyo", ".o", ".a", ".so", ".dylib", ".dll", ".exe", ".test":
		return true
	}
	return false
}
