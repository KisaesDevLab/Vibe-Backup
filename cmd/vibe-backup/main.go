// Command vibe-backup is the backup and verified-restore sidecar for the
// Vibe product family. See vibe-backup-BUILD-PLAN.md for the full design.
//
// BK-0 scaffold: the binary reports its version and serves the health and
// ping endpoints the appliance manifest declares, so the published image
// runs truthfully as a container. The orchestrator lands in BK-2.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
)

// Injected at build time via -ldflags (see Dockerfile).
var (
	version   = "dev"
	gitSHA    = "unknown"
	buildDate = "unknown"
)

const defaultAddr = ":4000"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "version":
		fmt.Printf("vibe-backup %s (commit %s, built %s)\n", version, gitSHA, buildDate)
	case "serve":
		if err := serve(); err != nil {
			log.Fatal(err)
		}
	default:
		fmt.Fprintf(os.Stderr, "vibe-backup: unknown command %q (want: serve, version)\n", cmd)
		os.Exit(2)
	}
}

func serve() error {
	addr := os.Getenv("VIBE_BACKUP_ADDR")
	if addr == "" {
		addr = defaultAddr
	}

	log.Printf("vibe-backup %s listening on %s", version, addr)
	return http.ListenAndServe(addr, newMux())
}

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": version,
			"phase":   "BK-0 scaffold — orchestrator not yet implemented",
		})
	})
	return mux
}
