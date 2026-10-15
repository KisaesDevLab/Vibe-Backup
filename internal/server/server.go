// Package server is the self-contained UI + API of BK-10. In appliance
// mode the console proxies to it (BK-10a); standalone it sits behind
// whatever ingress the deployment uses. It performs no authentication
// itself — never expose it directly to a network without an
// authenticating proxy in front (documented in docs/operator-quickstart.md).
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/KisaesDevLab/Vibe-Backup/internal/engine"
	"github.com/KisaesDevLab/Vibe-Backup/internal/health"
	"github.com/KisaesDevLab/Vibe-Backup/internal/kit"
	"github.com/KisaesDevLab/Vibe-Backup/internal/state"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	Engine  *engine.Engine
	Version string

	// busy serializes long operations: one backup/drill/restore at a time.
	busy sync.Mutex
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("GET /api/kit.pdf", s.handleKitPDF)
	mux.HandleFunc("POST /api/kit/confirm", s.handleKitConfirm)
	mux.HandleFunc("POST /api/notify/email", s.handleNotifyEmail)
	mux.HandleFunc("POST /api/notify/test", s.handleNotifyTest)
	mux.HandleFunc("POST /api/backup/run", s.handleBackupRun)
	mux.HandleFunc("POST /api/drill/run", s.handleDrillRun)
	mux.HandleFunc("GET /api/snapshots", s.handleSnapshots)
	mux.HandleFunc("GET /api/restore/file", s.handleRestoreFile)
	mux.HandleFunc("POST /api/restore/module", s.handleRestoreModule)
	mux.HandleFunc("GET /api/evidence", s.handleEvidence)

	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /", http.FileServerFS(sub))
	return mux
}

func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, err error) {
	jsonOut(w, code, map[string]string{"error": err.Error()})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	jsonOut(w, 200, map[string]string{"status": "ok", "version": s.Version})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	st := s.Engine.Store.View()
	hs := health.Evaluate(st, s.Engine.Cfg.LocalRepoDir != "", time.Now())
	out := map[string]any{
		"health":     hs,
		"firmName":   st.FirmName,
		"gate":       st.Gate,
		"custodians": st.Custodians,
		"backups":    tail(st.LastBackups, 10),
		"drills":     tailDrills(st.LastDrills, 6),
	}
	out["applianceId"] = st.ApplianceID
	out["notify"] = map[string]any{
		"email":          st.NotifyEmail,
		"mailerReady":    s.Engine.Notify != nil,
		"pingConfigured": s.Engine.Cfg.PingURL != "",
	}
	jsonOut(w, 200, out)
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FirmName    string `json:"firmName"`
		ApplianceID string `json:"applianceId"`
		B2KeyID     string `json:"b2KeyId"`
		B2Key       string `json:"b2Key"`
		B2Bucket    string `json:"b2Bucket"`
		NotifyEmail string `json:"notifyEmail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonErr(w, 400, err)
		return
	}
	err := s.Engine.Setup(r.Context(), engine.SetupInput{
		FirmName:      in.FirmName,
		ApplianceID:   in.ApplianceID,
		B2MasterKeyID: in.B2KeyID,
		B2MasterKey:   in.B2Key,
		B2Bucket:      in.B2Bucket,
		B2KeyID:       in.B2KeyID,
		B2Key:         in.B2Key,
		NotifyEmail:   in.NotifyEmail,
		B2APIBase:     s.Engine.Cfg.B2API,
	})
	if err != nil {
		jsonErr(w, 400, err)
		return
	}
	jsonOut(w, 200, map[string]string{"next": "download the Recovery Kit at /api/kit.pdf, then confirm its activation code"})
}

func (s *Server) handleKitPDF(w http.ResponseWriter, _ *http.Request) {
	st := s.Engine.Store.View()
	// The kit downloads while a kit awaits confirmation (first setup or
	// rotation). Once active, re-download requires an explicit rotation —
	// the PDF is the master key and doesn't hang around as a casual file.
	if st.Gate != state.GateKitIssued {
		jsonErr(w, 403, fmt.Errorf("no kit is awaiting confirmation; the kit is only downloadable during activation (issue a rotation to re-key)"))
		return
	}
	d, err := s.Engine.KitData()
	if err != nil {
		jsonErr(w, 500, err)
		return
	}
	pdf, err := kit.RenderPDF(d)
	if err != nil {
		jsonErr(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=vibe-recovery-kit-gen%d.pdf", d.Generation))
	w.Write(pdf)
}

func (s *Server) handleKitConfirm(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code       string   `json:"code"`
		Custodians []string `json:"custodians"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonErr(w, 400, err)
		return
	}
	if err := s.Engine.ConfirmKit(in.Code, in.Custodians); err != nil {
		jsonErr(w, 400, err)
		return
	}
	s.Engine.Evidence("kit-confirmed", true, "activation gate passed")
	jsonOut(w, 200, map[string]string{"status": "active"})
}

func (s *Server) handleNotifyEmail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonErr(w, 400, err)
		return
	}
	email := strings.TrimSpace(in.Email)
	if email != "" && (!strings.Contains(email, "@") || strings.ContainsAny(email, " \r\n")) {
		jsonErr(w, 400, fmt.Errorf("that doesn't look like an email address"))
		return
	}
	err := s.Engine.Store.Mutate(func(st *state.State, _ *state.Secrets) error {
		st.NotifyEmail = email
		return nil
	})
	if err != nil {
		jsonErr(w, 500, err)
		return
	}
	jsonOut(w, 200, map[string]string{"email": email})
}

// handleNotifyTest sends a test alert immediately (no cooldown) so the
// operator can prove the pipeline before they need it.
func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	st := s.Engine.Store.View()
	if s.Engine.Notify == nil {
		jsonErr(w, 400, fmt.Errorf("no mail server is configured on this appliance (VIBE_BACKUP_SMTP_HOST) — ask whoever installed Vibe to set one, or rely on the console and dead-man ping"))
		return
	}
	if st.NotifyEmail == "" {
		jsonErr(w, 400, fmt.Errorf("set a notification email first"))
		return
	}
	err := s.Engine.Notify.Send(st.NotifyEmail, "Vibe Backup: test alert",
		"This is a test of Vibe Backup's failure alerts. If you are reading it, alerts for failed backups, failed restore drills, and Backblaze account problems will reach this address.\n\nNothing is wrong.")
	if err != nil {
		jsonErr(w, 500, fmt.Errorf("test email failed: %w", err))
		return
	}
	s.Engine.Evidence("alert-email-test", true, st.NotifyEmail)
	jsonOut(w, 200, map[string]string{"sent": st.NotifyEmail})
}

func (s *Server) handleBackupRun(w http.ResponseWriter, r *http.Request) {
	if !s.busy.TryLock() {
		jsonErr(w, 409, fmt.Errorf("another backup, drill, or restore is already running"))
		return
	}
	defer s.busy.Unlock()
	rec, err := s.Engine.RunBackup(r.Context())
	if err != nil {
		jsonErr(w, 500, err)
		return
	}
	jsonOut(w, 200, rec)
}

func (s *Server) handleDrillRun(w http.ResponseWriter, r *http.Request) {
	if !s.busy.TryLock() {
		jsonErr(w, 409, fmt.Errorf("another backup, drill, or restore is already running"))
		return
	}
	defer s.busy.Unlock()
	rec := s.Engine.RunDrill(r.Context())
	code := 200
	if !rec.OK {
		code = 500
	}
	jsonOut(w, code, rec)
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	snaps, err := s.Engine.ListSnapshots(r.Context())
	if err != nil {
		jsonErr(w, 500, err)
		return
	}
	jsonOut(w, 200, snaps)
}

func (s *Server) handleRestoreFile(w http.ResponseWriter, r *http.Request) {
	snap := r.URL.Query().Get("snapshot")
	path := r.URL.Query().Get("path")
	if snap == "" || path == "" {
		jsonErr(w, 400, fmt.Errorf("snapshot and path are required"))
		return
	}
	data, err := s.Engine.RestoreFile(r.Context(), snap, path)
	if err != nil {
		jsonErr(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(path))
	w.Write(data)
}

func (s *Server) handleRestoreModule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Snapshot string `json:"snapshot"`
		Module   string `json:"module"`
		// Confirm must equal the module name, typed by a human (§6 T1).
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonErr(w, 400, err)
		return
	}
	if strings.TrimSpace(in.Confirm) != in.Module || in.Module == "" {
		jsonErr(w, 400, fmt.Errorf("type the module name exactly (%q) to confirm this restore — it will replace the module's current data", in.Module))
		return
	}
	if !s.busy.TryLock() {
		jsonErr(w, 409, fmt.Errorf("another backup, drill, or restore is already running"))
		return
	}
	defer s.busy.Unlock()
	res, err := s.Engine.RestoreModule(r.Context(), in.Snapshot, in.Module)
	if err != nil {
		jsonErr(w, 500, err)
		return
	}
	jsonOut(w, 200, res)
}

func (s *Server) handleEvidence(w http.ResponseWriter, _ *http.Request) {
	raw, err := os.ReadFile(filepath.Join(s.Engine.Cfg.StateDir, "evidence.jsonl"))
	if err != nil {
		jsonOut(w, 200, []any{})
		return
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	var out []json.RawMessage
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, json.RawMessage(l))
		}
	}
	jsonOut(w, 200, out)
}

func tail(rs []state.RunRecord, n int) []state.RunRecord {
	if len(rs) > n {
		rs = rs[len(rs)-n:]
	}
	out := make([]state.RunRecord, len(rs))
	copy(out, rs)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func tailDrills(rs []state.DrillRecord, n int) []state.DrillRecord {
	if len(rs) > n {
		rs = rs[len(rs)-n:]
	}
	out := make([]state.DrillRecord, len(rs))
	copy(out, rs)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Serve runs until ctx is done.
func Serve(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	log.Printf("vibe-backup listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
