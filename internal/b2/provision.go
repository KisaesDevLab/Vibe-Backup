package b2

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// Provisioned is everything the wizard produces. The master key that
// created it is gone by the time this struct exists.
type Provisioned struct {
	AccountID    string
	Bucket       Bucket
	ApplianceKey Key // list/read/write, no delete — daily backups
	RestoreKey   Key // read-only — printed on the Recovery Kit
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// BucketName derives a valid, globally-unique-enough B2 bucket name.
func BucketName(firmName string) string { return bucketName("vibe-backup", firmName) }

// FilerBucketName names the firm's document bucket (BK-5a).
func FilerBucketName(firmName string) string { return bucketName("vibe-filer", firmName) }

func bucketName(prefix, firmName string) string {
	slug := slugRe.ReplaceAllString(strings.ToLower(firmName), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 30 {
		slug = slug[:30]
	}
	suffix := make([]byte, 3)
	rand.Read(suffix)
	return fmt.Sprintf("%s-%s-%s", prefix, slug, hex.EncodeToString(suffix))
}

// Provision runs the BK-5 flow against a firm-owned account:
//
//  1. authorize with the MASTER key (held in memory only for this call),
//  2. create the locked backup bucket (Object Lock, compliance, 90d),
//  3. mint the no-delete appliance key and the read-only restore key,
//  4. return — the caller persists the results and lets the master key
//     leave memory. It is never written anywhere.
//
// The operator never opens the Backblaze console (design rule 1).
func Provision(ctx context.Context, c *Client, masterKeyID, masterKey, firmName string) (*Provisioned, error) {
	if err := c.Authorize(ctx, masterKeyID, masterKey); err != nil {
		return nil, fmt.Errorf("b2 provisioning: the master key was not accepted: %w", err)
	}

	bkt, err := c.CreateLockedBucket(ctx, BucketName(firmName))
	if err != nil {
		return nil, fmt.Errorf("b2 provisioning: create bucket: %w", err)
	}
	if !bkt.FileLockConfiguration.Value.IsFileLockEnabled {
		return nil, fmt.Errorf("b2 provisioning: bucket %s reports Object Lock DISABLED — refusing to continue without immutability", bkt.Name)
	}

	appKey, err := c.CreateKey(ctx, "vibe-backup-appliance", bkt.ID, ApplianceCapabilities)
	if err != nil {
		return nil, fmt.Errorf("b2 provisioning: mint appliance key: %w", err)
	}
	if err := assertNoDelete(appKey); err != nil {
		return nil, err
	}

	restoreKey, err := c.CreateKey(ctx, "vibe-backup-restore", bkt.ID, RestoreCapabilities)
	if err != nil {
		return nil, fmt.Errorf("b2 provisioning: mint restore key: %w", err)
	}
	if err := assertNoDelete(restoreKey); err != nil {
		return nil, err
	}

	return &Provisioned{
		AccountID:    c.AccountID(),
		Bucket:       *bkt,
		ApplianceKey: *appKey,
		RestoreKey:   *restoreKey,
	}, nil
}

// assertNoDelete verifies the capability grant the API actually returned —
// not what we asked for. First line of the BK-7 red-team.
func assertNoDelete(k *Key) error {
	for _, cap := range k.Capabilities {
		if strings.HasPrefix(cap, "delete") {
			return fmt.Errorf("b2 provisioning: key %s was granted %s — the appliance must provably be unable to delete; refusing this key", k.Name, cap)
		}
	}
	return nil
}

// FilerProvisioned is BK-5a's output: the firm's document bucket, locked
// from birth, and the no-delete key Filer will write with. The installer
// injects both into Filer's environment.
type FilerProvisioned struct {
	Bucket Bucket
	Key    Key
}

// ProvisionFilerBucket creates the Filer document bucket in the same
// wizard pass as the backup bucket (decided 2026-08-29, QUESTIONS.md #8):
// Object Lock compliance-mode retention on document objects, a lifecycle
// rule expiring noncurrent versions, and a write-only (no-delete) key. A
// compromised appliance cannot delete the firm's stored documents.
func ProvisionFilerBucket(ctx context.Context, c *Client, firmName string, noncurrentDays int) (*FilerProvisioned, error) {
	bkt, err := c.CreateLockedBucket(ctx, FilerBucketName(firmName))
	if err != nil {
		return nil, fmt.Errorf("b2 filer provisioning: create bucket: %w", err)
	}
	if !bkt.FileLockConfiguration.Value.IsFileLockEnabled {
		return nil, fmt.Errorf("b2 filer provisioning: bucket %s reports Object Lock DISABLED — refusing", bkt.Name)
	}
	if err := c.SetLifecycle(ctx, bkt.ID, noncurrentDays); err != nil {
		return nil, fmt.Errorf("b2 filer provisioning: lifecycle rules: %w", err)
	}
	key, err := c.CreateKey(ctx, "vibe-filer", bkt.ID, FilerCapabilities)
	if err != nil {
		return nil, fmt.Errorf("b2 filer provisioning: mint key: %w", err)
	}
	if err := assertNoDelete(key); err != nil {
		return nil, err
	}
	return &FilerProvisioned{Bucket: *bkt, Key: *key}, nil
}

// VerifyCannotDelete is the runtime half of the BK-7 red-team: authorize
// AS the appliance key and attempt a delete. Success of the attack is a
// hard failure; the expected outcome is a 401 unauthorized.
func VerifyCannotDelete(ctx context.Context, apiBase, keyID, key string) error {
	c := New(apiBase)
	if err := c.Authorize(ctx, keyID, key); err != nil {
		return fmt.Errorf("b2 red-team: could not authorize as appliance key: %w", err)
	}
	err := c.call(ctx, "b2_delete_file_version", map[string]any{
		"fileName": "vibe-backup-redteam-probe",
		"fileId":   "4_zprobe_nonexistent",
	}, nil)
	if err == nil {
		return fmt.Errorf("b2 red-team: DELETE SUCCEEDED with the appliance key — immutability is broken, do not trust this configuration")
	}
	var ae *apiErr
	if asAPIErr(err, &ae) && ae.Status == 401 {
		return nil // unauthorized: exactly what we want
	}
	return fmt.Errorf("b2 red-team: delete failed but not with 401 unauthorized (%v) — verify the key's capabilities manually", err)
}
