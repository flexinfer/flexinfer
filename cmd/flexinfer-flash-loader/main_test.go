package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestShouldCopy_Missing(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.bin")
	dst := filepath.Join(t.TempDir(), "dst.bin")

	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !shouldCopy(src, dst) {
		t.Fatal("expected shouldCopy=true when destination is missing")
	}
}

func TestShouldCopy_SameSize(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")

	data := []byte("matching content")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if shouldCopy(src, dst) {
		t.Fatal("expected shouldCopy=false when sizes match")
	}
}

func TestShouldCopy_DifferentSize(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")

	if err := os.WriteFile(src, []byte("longer content here"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !shouldCopy(src, dst) {
		t.Fatal("expected shouldCopy=true when sizes differ")
	}
}

func TestCopyFileAtomic_LargeBuffer(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")

	// Create a test file with known content
	content := make([]byte, 64*1024) // 64KB
	for i := range content {
		content[i] = byte(i % 256)
	}
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 4*1024*1024) // 4MB buffer
	n, err := copyFileAtomic(src, dst, buf)
	if err != nil {
		t.Fatalf("copyFileAtomic failed: %v", err)
	}
	if n != int64(len(content)) {
		t.Fatalf("copied %d bytes, want %d", n, len(content))
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if len(got) != len(content) {
		t.Fatalf("destination size %d, want %d", len(got), len(content))
	}
	for i := range content {
		if got[i] != content[i] {
			t.Fatalf("byte mismatch at offset %d: got %d, want %d", i, got[i], content[i])
		}
	}
}

func TestCopyFileAtomic_NoPartialFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")

	if err := os.WriteFile(src, []byte("complete content"), 0o644); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 4096)
	_, err := copyFileAtomic(src, dst, buf)
	if err != nil {
		t.Fatalf("copyFileAtomic failed: %v", err)
	}

	// Verify no .flash-tmp file remains
	tmpPath := dst + ".flash-tmp"
	if _, err := os.Stat(tmpPath); err == nil {
		t.Fatal("expected .flash-tmp to not exist after successful copy")
	}
}

func TestCleanStaleTmpFiles(t *testing.T) {
	dir := t.TempDir()

	// Create some normal files and some .flash-tmp files
	if err := os.WriteFile(filepath.Join(dir, "model.bin"), []byte("model"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.bin.flash-tmp"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "weights.flash-tmp"), []byte("stale2"), 0o644); err != nil {
		t.Fatal(err)
	}

	cleanStaleTmpFiles(dir)

	// Normal file should remain
	if _, err := os.Stat(filepath.Join(dir, "model.bin")); err != nil {
		t.Fatal("expected model.bin to remain")
	}
	// Stale tmp files should be removed
	if _, err := os.Stat(filepath.Join(dir, "model.bin.flash-tmp")); err == nil {
		t.Fatal("expected model.bin.flash-tmp to be removed")
	}
	if _, err := os.Stat(filepath.Join(sub, "weights.flash-tmp")); err == nil {
		t.Fatal("expected subdir/weights.flash-tmp to be removed")
	}
}

func TestVerifyIntegrity_Pass(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create matching files
	sub := filepath.Join(srcDir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dstDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		"model.bin":       make([]byte, 100),
		"sub/config.json": []byte(`{"key": "value"}`),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(srcDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dstDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := verifyIntegrity(srcDir, dstDir, defaultExcludes, ""); err != nil {
		t.Fatalf("expected verification to pass: %v", err)
	}
}

func TestVerifyIntegrity_SizeMismatch(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(srcDir, "model.bin"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "model.bin"), make([]byte, 50), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := verifyIntegrity(srcDir, dstDir, defaultExcludes, ""); err == nil {
		t.Fatal("expected verification to fail on size mismatch")
	}
}

func TestVerifyIntegrity_MissingFile(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(srcDir, "model.bin"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Don't create the file in dstDir

	if err := verifyIntegrity(srcDir, dstDir, defaultExcludes, ""); err == nil {
		t.Fatal("expected verification to fail on missing file")
	}
}

// --- New resilience tests ---

func TestParseExcludes_Default(t *testing.T) {
	excludes := parseExcludes("")
	if len(excludes) != len(defaultExcludes) {
		t.Fatalf("expected %d defaults, got %d", len(defaultExcludes), len(excludes))
	}
	for i, e := range defaultExcludes {
		if excludes[i] != e {
			t.Fatalf("exclude[%d] = %q, want %q", i, excludes[i], e)
		}
	}
}

func TestParseExcludes_Custom(t *testing.T) {
	excludes := parseExcludes("onnx/,*.onnx")
	// Should contain defaults + custom
	if len(excludes) != len(defaultExcludes)+2 {
		t.Fatalf("expected %d excludes, got %d: %v", len(defaultExcludes)+2, len(excludes), excludes)
	}
}

func TestShouldExclude(t *testing.T) {
	excludes := []string{".cache/", ".git/", "onnx/"}

	tests := []struct {
		rel  string
		want bool
	}{
		{".cache/huggingface/hub", true},
		{".git/objects/pack", true},
		{"onnx/model.onnx", true},
		{"model.safetensors", false},
		{"sub/config.json", false},
		{"models/.cache/foo", false}, // .cache not at component boundary — but prefix-based
	}

	for _, tt := range tests {
		got := shouldExclude(tt.rel, excludes)
		if got != tt.want {
			t.Errorf("shouldExclude(%q) = %v, want %v", tt.rel, got, tt.want)
		}
	}
}

func TestFilterFP32Variants_Dedup(t *testing.T) {
	files := []fileEntry{
		{rel: "config.json", size: 100},
		{rel: "model.safetensors", size: 5000},
		{rel: "model.fp16.safetensors", size: 2500},
		{rel: "tokenizer.json", size: 200},
		{rel: "vae/diffusion_pytorch_model.safetensors", size: 3000},
		{rel: "vae/diffusion_pytorch_model.fp16.safetensors", size: 1500},
	}

	filtered := filterFP32Variants(files)

	// Should remove model.safetensors and vae/diffusion_pytorch_model.safetensors
	if len(filtered) != 4 {
		t.Fatalf("expected 4 files after filtering, got %d", len(filtered))
	}

	names := make(map[string]bool)
	for _, f := range filtered {
		names[f.rel] = true
	}

	if names["model.safetensors"] {
		t.Error("fp32 model.safetensors should have been filtered out")
	}
	if names["vae/diffusion_pytorch_model.safetensors"] {
		t.Error("fp32 vae/diffusion_pytorch_model.safetensors should have been filtered out")
	}
	if !names["model.fp16.safetensors"] {
		t.Error("fp16 model.fp16.safetensors should be kept")
	}
	if !names["config.json"] {
		t.Error("config.json should be kept")
	}
}

func TestFilterFP32Variants_NoFP16(t *testing.T) {
	files := []fileEntry{
		{rel: "model.safetensors", size: 5000},
		{rel: "config.json", size: 100},
	}

	filtered := filterFP32Variants(files)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 files (no fp16 to dedup against), got %d", len(filtered))
	}
}

func TestCheckAvailableSpace_Sufficient(t *testing.T) {
	dir := t.TempDir()
	// tmpdir should have plenty of space
	err := checkAvailableSpace(dir, 1024)
	if err != nil {
		t.Fatalf("expected space check to pass: %v", err)
	}
}

func TestCheckAvailableSpace_NonexistentPath(t *testing.T) {
	// Non-existent path returns warning but no error (graceful fallback)
	err := checkAvailableSpace("/nonexistent/flash/test/path", 1024)
	if err != nil {
		t.Fatalf("expected graceful fallback on non-existent path, got: %v", err)
	}
}

func TestCheckAvailableSpace_Insufficient(t *testing.T) {
	dir := t.TempDir()
	// Request an impossibly large amount (100 PB)
	err := checkAvailableSpace(dir, 100*1024*1024*1024*1024*1024)
	if err == nil {
		t.Fatal("expected space check to fail for 100 PB")
	}
	if got := err.Error(); !contains(got, "insufficient tmpfs space") {
		t.Fatalf("expected 'insufficient tmpfs space' in error, got: %s", got)
	}
}

func TestVerifyIntegrity_ExcludesRespected(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create files in source including excluded ones
	if err := os.MkdirAll(filepath.Join(srcDir, ".cache", "hub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, ".cache", "hub", "blob"), []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "model.bin"), []byte("model"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Only copy the non-excluded file
	if err := os.WriteFile(filepath.Join(dstDir, "model.bin"), []byte("model"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Should pass — .cache/ is excluded, so missing from dst is fine
	if err := verifyIntegrity(srcDir, dstDir, defaultExcludes, ""); err != nil {
		t.Fatalf("verification should pass when excluded files are missing from dst: %v", err)
	}
}

func TestVerifyIntegrity_FP16VariantFilter(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Source has both fp32 and fp16
	if err := os.WriteFile(filepath.Join(srcDir, "model.safetensors"), make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "model.fp16.safetensors"), make([]byte, 2500), 0o644); err != nil {
		t.Fatal(err)
	}

	// Destination only has fp16
	if err := os.WriteFile(filepath.Join(dstDir, "model.fp16.safetensors"), make([]byte, 2500), 0o644); err != nil {
		t.Fatal(err)
	}

	// Should pass with variant=fp16 (fp32 file excluded)
	if err := verifyIntegrity(srcDir, dstDir, defaultExcludes, "fp16"); err != nil {
		t.Fatalf("verification should pass with fp16 variant filter: %v", err)
	}

	// Should fail without variant filter (fp32 file expected)
	if err := verifyIntegrity(srcDir, dstDir, defaultExcludes, ""); err == nil {
		t.Fatal("verification should fail without variant filter (fp32 missing from dst)")
	}
}

func TestSymlinkSkipping(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks not reliable on Windows")
	}

	srcDir := t.TempDir()

	// Create a regular file and a symlink
	if err := os.WriteFile(filepath.Join(srcDir, "real.bin"), []byte("real data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(srcDir, "real.bin"), filepath.Join(srcDir, "link.bin")); err != nil {
		t.Fatal(err)
	}

	// Create a symlink to a non-existent target (broken symlink)
	if err := os.Symlink("/nonexistent/target", filepath.Join(srcDir, "broken.bin")); err != nil {
		t.Fatal(err)
	}

	// Walk with our filtering — should find only real.bin
	var files []fileEntry
	var symlinkCount int
	_ = filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(srcDir, path)
		if rel != "." {
			linfo, lerr := os.Lstat(path)
			if lerr == nil && linfo.Mode()&os.ModeSymlink != 0 {
				symlinkCount++
				return nil
			}
		}
		if !info.IsDir() {
			files = append(files, fileEntry{rel: rel, size: info.Size()})
		}
		return nil
	})

	if len(files) != 1 {
		t.Fatalf("expected 1 regular file, got %d: %v", len(files), files)
	}
	if files[0].rel != "real.bin" {
		t.Fatalf("expected real.bin, got %s", files[0].rel)
	}
	if symlinkCount != 2 {
		t.Fatalf("expected 2 symlinks detected, got %d", symlinkCount)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsImpl(s, substr))
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
