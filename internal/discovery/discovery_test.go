package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KisaesDevLab/Vibe-Backup/internal/dockerapi"
)

type fakeDocker struct {
	containers []dockerapi.Container
	files      map[string][]byte // containerID+path -> content
}

func (f *fakeDocker) ListByLabel(_ context.Context, _ string) ([]dockerapi.Container, error) {
	return f.containers, nil
}

func (f *fakeDocker) Inspect(_ context.Context, id string) (*dockerapi.ContainerDetail, error) {
	d := &dockerapi.ContainerDetail{ID: id}
	d.Config.Image = "ghcr.io/kisaesdevlab/fake:1.2.3"
	return d, nil
}

func (f *fakeDocker) ReadFile(_ context.Context, id, path string, _ int64) ([]byte, error) {
	b, ok := f.files[id+path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return b, nil
}

func container(id, name, contractPath string) dockerapi.Container {
	return dockerapi.Container{
		ID:     id,
		Names:  []string{"/" + name},
		Labels: map[string]string{"vibe.backup.contract": contractPath},
	}
}

const goodContract = "module: vibe-fake\nschema: 1\ndatabases: [fake_db]\n"

func TestDiscoverHappyPath(t *testing.T) {
	fd := &fakeDocker{
		containers: []dockerapi.Container{container("c1", "fake", "/app/backup.contract.yaml")},
		files:      map[string][]byte{"c1/app/backup.contract.yaml": []byte(goodContract)},
	}
	mods, err := Discover(context.Background(), fd)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].Contract.Module != "vibe-fake" || mods[0].ImageRef == "" {
		t.Fatalf("bad discovery: %+v", mods)
	}
}

func TestUnreadableContractIsErrorNotSkip(t *testing.T) {
	fd := &fakeDocker{
		containers: []dockerapi.Container{container("c1", "fake", "/app/backup.contract.yaml")},
		files:      map[string][]byte{},
	}
	if _, err := Discover(context.Background(), fd); err == nil {
		t.Fatal("labeled container with unreadable contract must fail the run")
	}
}

func TestDuplicateModuleIsError(t *testing.T) {
	fd := &fakeDocker{
		containers: []dockerapi.Container{
			container("c1", "a", "/x.yaml"),
			container("c2", "b", "/x.yaml"),
		},
		files: map[string][]byte{
			"c1/x.yaml": []byte(goodContract),
			"c2/x.yaml": []byte(goodContract),
		},
	}
	if _, err := Discover(context.Background(), fd); err == nil || !strings.Contains(err.Error(), "declared by both") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestCrossCheckCatchesGap(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "modules.json")
	os.WriteFile(p, []byte(`{"modules":[{"slug":"vibe-fake"},{"slug":"vibe-ghost"}]}`), 0o644)

	fd := &fakeDocker{
		containers: []dockerapi.Container{container("c1", "fake", "/x.yaml")},
		files:      map[string][]byte{"c1/x.yaml": []byte(goodContract)},
	}
	mods, err := Discover(context.Background(), fd)
	if err != nil {
		t.Fatal(err)
	}
	if err := CrossCheck(p, mods); err == nil || !strings.Contains(err.Error(), "vibe-ghost") {
		t.Fatalf("want gap error naming vibe-ghost, got %v", err)
	}
}

func TestCrossCheckStandaloneNoManifest(t *testing.T) {
	if err := CrossCheck("", nil); err != nil {
		t.Fatal("standalone mode must not require a manifest")
	}
}
