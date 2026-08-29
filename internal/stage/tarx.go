package stage

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractTar unpacks a Docker archive stream into dst, stripping the
// leading path element (Docker roots the archive at the requested
// directory's basename). Guards against path traversal; preserves
// symlinks; skips devices and sockets.
func extractTar(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := stripFirst(hdr.Name)
		if name == "" {
			continue
		}
		if strings.Contains(name, "..") {
			return fmt.Errorf("stage: tar entry escapes extraction root: %q", hdr.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			linked := filepath.Join(dst, filepath.FromSlash(stripFirst(hdr.Linkname)))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Link(linked, target); err != nil {
				return err
			}
		default:
			// Devices, FIFOs, sockets: nothing a backup should carry.
		}
	}
}

func stripFirst(name string) string {
	name = strings.TrimPrefix(filepath.ToSlash(name), "/")
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return ""
}
