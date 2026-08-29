// Package discovery enumerates modules to back up. Discovery is by Docker
// label, not by manifest (plan D-9), so it works identically in appliance
// and standalone modes. When the appliance manifest is present, it is used
// as a cross-check only: a manifest module with no labeled container is a
// hard error — that is how a module would otherwise ship unprotected while
// the console shows green.
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/KisaesDevLab/Vibe-Backup/internal/contract"
	"github.com/KisaesDevLab/Vibe-Backup/internal/dockerapi"
)

// Module pairs a parsed contract with the container it came from.
type Module struct {
	Contract      *contract.Contract
	ContainerID   string
	ContainerName string
	// ImageRef is the image reference the container runs (manifest item 7:
	// exact tags are recorded so restore pulls what was dumped from).
	ImageRef string
}

type Docker interface {
	ListByLabel(ctx context.Context, label string) ([]dockerapi.Container, error)
	Inspect(ctx context.Context, id string) (*dockerapi.ContainerDetail, error)
	ReadFile(ctx context.Context, id, path string, maxSize int64) ([]byte, error)
}

const maxContractSize = 256 << 10

// Discover returns every labeled module's contract. A labeled container
// with an unreadable or invalid contract is an error, not a skip (§4.2).
func Discover(ctx context.Context, d Docker) ([]Module, error) {
	containers, err := d.ListByLabel(ctx, contract.Label)
	if err != nil {
		return nil, fmt.Errorf("discovery: list containers: %w", err)
	}
	var mods []Module
	seen := map[string]string{} // module name -> container, to catch duplicates
	for _, ct := range containers {
		path := ct.Labels[contract.Label]
		if strings.TrimSpace(path) == "" {
			return nil, fmt.Errorf("discovery: container %s carries %s with an empty path", ct.Name(), contract.Label)
		}
		raw, err := d.ReadFile(ctx, ct.ID, path, maxContractSize)
		if err != nil {
			return nil, fmt.Errorf("discovery: container %s: cannot read contract %s: %w", ct.Name(), path, err)
		}
		c, err := contract.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("discovery: container %s: %w", ct.Name(), err)
		}
		if prev, dup := seen[c.Module]; dup {
			return nil, fmt.Errorf("discovery: module %q declared by both %s and %s", c.Module, prev, ct.Name())
		}
		seen[c.Module] = ct.Name()

		detail, err := d.Inspect(ctx, ct.ID)
		if err != nil {
			return nil, fmt.Errorf("discovery: inspect %s: %w", ct.Name(), err)
		}
		mods = append(mods, Module{
			Contract:      c,
			ContainerID:   ct.ID,
			ContainerName: ct.Name(),
			ImageRef:      detail.Config.Image,
		})
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Contract.Module < mods[j].Contract.Module })
	return mods, nil
}

// applianceManifest is the minimal slice of the meta-installer's
// installed-modules file we need for the cross-check.
type applianceManifest struct {
	Modules []struct {
		Slug string `json:"slug"`
	} `json:"modules"`
}

// CrossCheck fails when a module the appliance says is installed has no
// discovered contract (appliance mode only; plan §4.2).
func CrossCheck(manifestPath string, mods []Module) error {
	if manifestPath == "" {
		return nil // standalone mode: labels are the only source of truth
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("discovery: appliance manifest unreadable: %w", err)
	}
	var man applianceManifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return fmt.Errorf("discovery: appliance manifest: %w", err)
	}
	have := map[string]bool{}
	for _, m := range mods {
		have[m.Contract.Module] = true
	}
	var missing []string
	for _, im := range man.Modules {
		if !have[im.Slug] {
			missing = append(missing, im.Slug)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("discovery: installed modules with no backup contract: %s — refusing to run a backup that silently excludes them", strings.Join(missing, ", "))
	}
	return nil
}
