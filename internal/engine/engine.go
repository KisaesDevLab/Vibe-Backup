// Package engine composes discovery, staging, restic, and state into the
// operations the scheduler and the UI both call: run a backup, run a
// drill, rotate generations. One Engine per process.
package engine

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/KisaesDevLab/Vibe-Backup/internal/config"
	"github.com/KisaesDevLab/Vibe-Backup/internal/discovery"
	"github.com/KisaesDevLab/Vibe-Backup/internal/dockerapi"
	"github.com/KisaesDevLab/Vibe-Backup/internal/notify"
	"github.com/KisaesDevLab/Vibe-Backup/internal/pairing"
	"github.com/KisaesDevLab/Vibe-Backup/internal/resticw"
	"github.com/KisaesDevLab/Vibe-Backup/internal/stage"
	"github.com/KisaesDevLab/Vibe-Backup/internal/state"
)

type Engine struct {
	Cfg     config.Config
	Docker  *dockerapi.Client
	Store   *state.Store
	Restic  *resticw.Runner
	Version string
	// Notify sends failure alert emails; nil = email alerting off.
	Notify notify.Sender
}

// ErrGateClosed is returned when backups are attempted before the
// Recovery Kit is confirmed (BK-6 exit criterion).
var ErrGateClosed = fmt.Errorf("backups have not been activated: download the Recovery Kit and type its six-word activation code into the console first")

// Repos assembles the destination repositories for the ACTIVE generation.
// Either may be nil (unconfigured); both nil is an error at call sites.
func (e *Engine) Repos() (local, remote *resticw.Repo, err error) {
	st := e.Store.View()
	sec := e.Store.SecretsView()
	gen := activeGeneration(&st)
	if gen == nil {
		return nil, nil, fmt.Errorf("no active generation — setup has not completed")
	}
	pass := sec.Passphrases[fmt.Sprint(gen.Number)]
	if pass == "" {
		return nil, nil, fmt.Errorf("generation %d has no stored passphrase — state is corrupt, re-run setup", gen.Number)
	}
	if e.Cfg.LocalRepoDir != "" {
		local = &resticw.Repo{
			Name:     "local",
			URL:      filepath.Join(e.Cfg.LocalRepoDir, fmt.Sprintf("gen%d", gen.Number)),
			Password: pass,
			Prunable: true,
		}
	}
	if gen.B2Bucket != "" {
		remote = &resticw.Repo{
			Name:     "b2",
			URL:      fmt.Sprintf("b2:%s:%s", gen.B2Bucket, gen.B2Prefix),
			Password: pass,
			Env: map[string]string{
				"B2_ACCOUNT_ID":  st.B2KeyID,
				"B2_ACCOUNT_KEY": sec.B2AppKey,
			},
		}
	}
	return local, remote, nil
}

// RunBackup executes one full backup: discover → stage → verify pairing →
// snapshot to every destination → prune local → record + evidence.
func (e *Engine) RunBackup(ctx context.Context) (rec state.RunRecord, err error) {
	rec.At = time.Now().UTC()
	defer func() {
		if err != nil {
			rec.OK = false
			rec.Error = err.Error()
		}
		e.Store.Mutate(func(st *state.State, _ *state.Secrets) error {
			st.RecordBackup(rec)
			return nil
		})
		e.Evidence("backup", rec.OK, rec.Error)
		if rec.OK {
			e.ping(ctx)
		} else if err != ErrGateClosed {
			e.Alert("backup-failure", "Vibe Backup: a backup FAILED",
				"Tonight's backup did not complete.\n\nWhat went wrong:\n"+rec.Error+
					"\n\nYour previous backups are unaffected. If this repeats, the console's Backup page shows the specific fix.")
		}
	}()

	st := e.Store.View()
	if st.Gate != state.GateActive {
		return rec, ErrGateClosed
	}
	local, remote, err := e.Repos()
	if err != nil {
		return rec, err
	}
	if local == nil && remote == nil {
		return rec, fmt.Errorf("no backup destination configured (neither local repository nor B2)")
	}

	mods, err := discovery.Discover(ctx, e.Docker)
	if err != nil {
		return rec, err
	}
	if err := discovery.CrossCheck(e.Cfg.ApplianceManifest, mods); err != nil {
		return rec, err
	}
	if len(mods) == 0 && len(e.Cfg.ExtraPaths) == 0 {
		return rec, fmt.Errorf("nothing to back up: no labeled module containers found and no extra paths configured")
	}

	gen := activeGeneration(&st)
	stageDir := filepath.Join(e.Cfg.StateDir, "staging", rec.At.Format("20060102-150405"))
	defer os.RemoveAll(stageDir)

	stager := &stage.Stager{
		Docker:      e.Docker,
		PGContainer: e.Cfg.PGContainer,
		ExtraPaths:  e.Cfg.ExtraPaths,
		Generation:  gen.Number,
		AppVersion:  e.Version,
	}
	man, err := stager.Build(ctx, stageDir, mods)
	if err != nil {
		return rec, err
	}
	if _, err := pairing.Verify(stageDir); err != nil {
		return rec, err
	}
	rec.SnapshotID = man.SnapshotID

	tags := map[string]string{
		"vibe-snapshot":   man.SnapshotID,
		"vibe-generation": fmt.Sprint(gen.Number),
	}
	for _, repo := range []*resticw.Repo{local, remote} {
		if repo == nil {
			continue
		}
		if err := e.Restic.Init(ctx, *repo); err != nil {
			return rec, err
		}
		if _, err := e.Restic.Backup(ctx, *repo, stageDir, tags); err != nil {
			return rec, err
		}
		rec.Destinations = append(rec.Destinations, repo.Name)
	}
	if local != nil {
		if err := e.Restic.ForgetAndPrune(ctx, *local); err != nil {
			// Prune failure doesn't invalidate the snapshot just taken.
			log.Printf("backup: local prune failed (snapshot is safe): %v", err)
		}
	}
	if local != nil && remote == nil {
		log.Printf("backup: WARNING — local destination only; an off-site copy is what survives fire, theft, and ransomware (§2)")
	}
	rec.OK = true
	return rec, nil
}

// ping fires the dead-man's-switch signal (BK-11).
func (e *Engine) ping(ctx context.Context) {
	if e.Cfg.PingURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", e.Cfg.PingURL, nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("deadman ping failed: %v", err)
		return
	}
	resp.Body.Close()
}

// Evidence appends one line to the compliance evidence log (JSONL) that
// Sentinel ingests (BK-12 feed side).
func (e *Engine) Evidence(kind string, ok bool, detail string) {
	f, err := os.OpenFile(filepath.Join(e.Cfg.StateDir, "evidence.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("evidence log: %v", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, `{"at":%q,"type":%q,"ok":%t,"detail":%q}`+"\n",
		time.Now().UTC().Format(time.RFC3339), kind, ok, detail)
}

// activeGeneration is the newest generation whose kit is CONFIRMED.
// A pending generation (rotation issued, kit not yet confirmed) does not
// receive writes — the previous generation keeps running until the
// operator passes the gate (D-4).
func activeGeneration(st *state.State) *state.Generation {
	for i := len(st.Generations) - 1; i >= 0; i-- {
		if !st.Generations[i].KitConfirmedAt.IsZero() {
			return &st.Generations[i]
		}
	}
	return nil
}

// ActiveGeneration exposes the rule for other packages.
func ActiveGeneration(st *state.State) *state.Generation { return activeGeneration(st) }
