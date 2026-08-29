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
func BucketName(firmName string) string {
	slug := slugRe.ReplaceAllString(strings.ToLower(firmName), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 30 {
		slug = slug[:30]
	}
	suffix := make([]byte, 3)
	rand.Read(suffix)
	return fmt.Sprintf("vibe-backup-%s-%s", slug, hex.EncodeToString(suffix))
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
