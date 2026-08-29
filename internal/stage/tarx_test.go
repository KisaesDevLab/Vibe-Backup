package stage

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarball(entries map[string]string) *bytes.Buffer {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range entries {
		if strings.HasSuffix(name, "/") {
			tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755})
			continue
		}
		tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))})
		tw.Write([]byte(content))
	}
	tw.Close()
	return &buf
}

func TestExtractStripsRoot(t *testing.T) {
	dst := t.TempDir()
	buf := tarball(map[string]string{
		"uploads/":          "",
		"uploads/a.txt":     "alpha",
		"uploads/sub/b.txt": "beta",
	})
	if err := extractTar(buf, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "sub", "b.txt"))
	if err != nil || string(got) != "beta" {
		t.Fatalf("extract failed: %v %q", err, got)
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	dst := t.TempDir()
	buf := tarball(map[string]string{"uploads/../../evil.txt": "pwn"})
	if err := extractTar(buf, dst); err == nil {
		t.Fatal("path traversal must be rejected")
	}
}
