// Package pairing implements BK-3a: proof that every artifact in a staged
// snapshot — database dumps AND the key material that decrypts them — was
// captured in the same staging transaction. A snapshot whose DB and env
// came from different moments is a corrupt backup that looks healthy
// (build plan §4.1, Class 1); pairing.json is what makes that detectable.
//
// pairing.json is written LAST, after every other file is in place. Its
// absence, or any file added, removed, or changed since it was written,
// fails verification — and verification runs before restore and during
// every drill.
package pairing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const FileName = "pairing.json"

type Entry struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Pairing struct {
	SnapshotID string           `json:"snapshotId"`
	StartedAt  time.Time        `json:"startedAt"`
	FinishedAt time.Time        `json:"finishedAt"`
	Files      map[string]Entry `json:"files"`
}

// Seal hashes every file under stageDir (except pairing.json itself) and
// writes pairing.json. Call it only when the stage is complete.
func Seal(stageDir, snapshotID string, startedAt time.Time) (*Pairing, error) {
	files, err := hashTree(stageDir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("pairing: refusing to seal an empty stage")
	}
	p := &Pairing{
		SnapshotID: snapshotID,
		StartedAt:  startedAt.UTC(),
		FinishedAt: time.Now().UTC(),
		Files:      files,
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(stageDir, FileName), b, 0o644); err != nil {
		return nil, err
	}
	return p, nil
}

// Verify recomputes the tree hash and compares it to pairing.json.
// Any divergence is a hard error naming the offending paths.
func Verify(stageDir string) (*Pairing, error) {
	raw, err := os.ReadFile(filepath.Join(stageDir, FileName))
	if err != nil {
		return nil, fmt.Errorf("pairing: %s missing or unreadable — snapshot cannot be proven atomic, refusing: %w", FileName, err)
	}
	var p Pairing
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("pairing: corrupt %s: %w", FileName, err)
	}
	actual, err := hashTree(stageDir)
	if err != nil {
		return nil, err
	}
	var problems []string
	for path, want := range p.Files {
		got, ok := actual[path]
		if !ok {
			problems = append(problems, "missing: "+path)
			continue
		}
		if got != want {
			problems = append(problems, "modified: "+path)
		}
	}
	for path := range actual {
		if _, ok := p.Files[path]; !ok {
			problems = append(problems, "unexpected: "+path)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("pairing: snapshot %s failed atomicity verification:\n  %s", p.SnapshotID, strings.Join(problems, "\n  "))
	}
	return &p, nil
}

func hashTree(root string) (map[string]Entry, error) {
	files := map[string]Entry{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == FileName {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		n, err := io.Copy(h, f)
		if err != nil {
			return err
		}
		files[rel] = Entry{SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}
		return nil
	})
	return files, err
}
