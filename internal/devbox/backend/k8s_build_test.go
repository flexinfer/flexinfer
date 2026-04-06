package backend

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestCleanupBuilds_DeletesOldCompletedPods(t *testing.T) {
	oldTime := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	recentTime := metav1.NewTime(time.Now().Add(-30 * time.Minute))

	clientset := k8sfake.NewSimpleClientset(
		// Old succeeded pod — should be deleted
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "buildah-build-old-succeeded",
				Namespace:         "devbox",
				Labels:            map[string]string{"devbox/build": "buildah"},
				CreationTimestamp: oldTime,
			},
			Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
		},
		// Old failed pod — should be deleted
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "buildah-build-old-failed",
				Namespace:         "devbox",
				Labels:            map[string]string{"devbox/build": "buildah"},
				CreationTimestamp: oldTime,
			},
			Status: corev1.PodStatus{Phase: corev1.PodFailed},
		},
		// Recent succeeded pod — should NOT be deleted
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "buildah-build-recent",
				Namespace:         "devbox",
				Labels:            map[string]string{"devbox/build": "buildah"},
				CreationTimestamp: recentTime,
			},
			Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
		},
		// Old running pod — should NOT be deleted
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "buildah-build-running",
				Namespace:         "devbox",
				Labels:            map[string]string{"devbox/build": "buildah"},
				CreationTimestamp: oldTime,
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		// Associated ConfigMap for old-succeeded
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "buildah-dockerfile-old-succeeded",
				Namespace: "devbox",
			},
		},
	)

	k := testK8sBackend()
	k.clientset = clientset

	cleaned, err := k.CleanupBuilds(context.Background(), 1*time.Hour)
	if err != nil {
		t.Fatalf("CleanupBuilds error: %v", err)
	}

	// 2 pods deleted (old-succeeded + old-failed)
	if cleaned != 2 {
		t.Errorf("expected 2 cleaned pods, got %d", cleaned)
	}

	// Verify remaining pods
	pods, _ := clientset.CoreV1().Pods("devbox").List(context.Background(), metav1.ListOptions{})
	remaining := make(map[string]bool)
	for _, p := range pods.Items {
		remaining[p.Name] = true
	}
	if remaining["buildah-build-old-succeeded"] {
		t.Error("old succeeded pod should have been deleted")
	}
	if remaining["buildah-build-old-failed"] {
		t.Error("old failed pod should have been deleted")
	}
	if !remaining["buildah-build-recent"] {
		t.Error("recent pod should NOT have been deleted")
	}
	if !remaining["buildah-build-running"] {
		t.Error("running pod should NOT have been deleted")
	}
}

func TestReadDepFiles_Empty(t *testing.T) {
	dir := t.TempDir()
	files := readDepFiles(dir)
	if len(files) != 0 {
		t.Errorf("expected empty map for empty dir, got %d files", len(files))
	}
}

func TestReadDepFiles_GoProject(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.25"), 0644)
	os.WriteFile(filepath.Join(dir, "go.sum"), []byte("hash1\nhash2"), 0644)

	files := readDepFiles(dir)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if _, ok := files["go.mod"]; !ok {
		t.Error("expected go.mod in result")
	}
	if _, ok := files["go.sum"]; !ok {
		t.Error("expected go.sum in result")
	}
}

func TestReadDepFiles_NodeProject(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"test"}`), 0644)
	os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfile"), 0644)

	files := readDepFiles(dir)
	if _, ok := files["package.json"]; !ok {
		t.Error("expected package.json")
	}
	if _, ok := files["pnpm-lock.yaml"]; !ok {
		t.Error("expected pnpm-lock.yaml")
	}
}

func TestReadDepFiles_MixedProject(t *testing.T) {
	dir := t.TempDir()
	// Simulate a project with both Go and Python deps.
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)
	os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]"), 0644)
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask==3.0"), 0644)

	files := readDepFiles(dir)
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d: %v", len(files), files)
	}
}

func TestReadDepFiles_NonExistentDir(t *testing.T) {
	files := readDepFiles("/nonexistent/path/for/testing")
	if len(files) != 0 {
		t.Errorf("expected empty map for nonexistent dir, got %d", len(files))
	}
}

func TestReadDepFiles_IgnoresNonDepFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0644)

	files := readDepFiles(dir)
	if len(files) != 0 {
		t.Errorf("expected 0 dep files, got %d: %v", len(files), files)
	}
}
