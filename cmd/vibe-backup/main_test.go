package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KisaesDevLab/Vibe-Backup/internal/config"
	"github.com/KisaesDevLab/Vibe-Backup/internal/dockerapi"
	"github.com/KisaesDevLab/Vibe-Backup/internal/engine"
	"github.com/KisaesDevLab/Vibe-Backup/internal/resticw"
	"github.com/KisaesDevLab/Vibe-Backup/internal/server"
	"github.com/KisaesDevLab/Vibe-Backup/internal/state"
)

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	docker, err := dockerapi.New("unix:///nonexistent.sock")
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &engine.Engine{
		Cfg:     config.Config{StateDir: t.TempDir()},
		Docker:  docker,
		Store:   store,
		Restic:  resticw.NewRunner(),
		Version: "test",
	}
	s := &server.Server{Engine: e, Version: "test"}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestPingAndHealth(t *testing.T) {
	srv := testServer(t)
	resp, err := http.Get(srv.URL + "/api/ping")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("ping: %d", resp.StatusCode)
	}
}

func TestStatusShowsSetupNeeded(t *testing.T) {
	srv := testServer(t)
	resp, err := http.Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Health struct {
			Level string `json:"level"`
		} `json:"health"`
		Gate string `json:"gate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Health.Level != "setup" || body.Gate != "unconfigured" {
		t.Fatalf("fresh install must ask for setup, got %+v", body)
	}
}

func TestUIIsServed(t *testing.T) {
	srv := testServer(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("UI index: %d", resp.StatusCode)
	}
}

func TestBackupRefusedBeforeActivation(t *testing.T) {
	srv := testServer(t)
	resp, err := http.Post(srv.URL+"/api/backup/run", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("backup must be refused before the activation gate")
	}
}
