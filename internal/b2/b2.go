// Package b2 talks to the Backblaze B2 native API for provisioning (BK-5)
// and account health (BK-11). The design invariants it enforces:
//
//   - The firm owns the account (D-2). The master key is accepted once,
//     used to provision, and never persisted — callers must discard it.
//   - The appliance's application key can list, read, and write but NEVER
//     delete. "Write-only" in the plan means "cannot destroy history";
//     restic needs read to function. The BK-7 red-team proves the absence
//     of deleteFiles.
//   - Object Lock is compliance mode, 90-day default retention (D-3/D-5):
//     nothing — not even the master key — can shorten it once set.
package b2

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultAPIBase = "https://api.backblazeb2.com"

// RetentionDays is the Object Lock default retention (D-5).
const RetentionDays = 90

type Client struct {
	// APIBase is overridable for tests.
	APIBase string
	HTTP    *http.Client

	auth *authResponse
}

type authResponse struct {
	AccountID          string `json:"accountId"`
	AuthorizationToken string `json:"authorizationToken"`
	APIURL             string `json:"apiUrl"`
}

type apiErr struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiErr) Error() string {
	return fmt.Sprintf("b2: %d %s: %s", e.Status, e.Code, e.Message)
}

func New(apiBase string) *Client {
	if apiBase == "" {
		apiBase = DefaultAPIBase
	}
	return &Client{APIBase: apiBase, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// Authorize exchanges a key pair for a session token. Works with the
// master key (wizard) or an application key (health check).
func (c *Client) Authorize(ctx context.Context, keyID, key string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.APIBase+"/b2api/v2/b2_authorize_account", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(keyID+":"+key)))
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("b2: authorize: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return parseErr(resp)
	}
	var auth authResponse
	if err := json.NewDecoder(resp.Body).Decode(&auth); err != nil {
		return fmt.Errorf("b2: authorize decode: %w", err)
	}
	c.auth = &auth
	return nil
}

func (c *Client) AccountID() string {
	if c.auth == nil {
		return ""
	}
	return c.auth.AccountID
}

func (c *Client) call(ctx context.Context, op string, body, out any) error {
	if c.auth == nil {
		return fmt.Errorf("b2: %s before authorize", op)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.auth.APIURL+"/b2api/v2/"+op, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.auth.AuthorizationToken)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("b2: %s: %w", op, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return parseErr(resp)
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func parseErr(resp *http.Response) error {
	var e apiErr
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if json.Unmarshal(raw, &e) != nil || e.Code == "" {
		e = apiErr{Status: resp.StatusCode, Code: "unknown", Message: strings.TrimSpace(string(raw))}
	}
	e.Status = resp.StatusCode
	return &e
}

type Bucket struct {
	ID                    string `json:"bucketId"`
	Name                  string `json:"bucketName"`
	Type                  string `json:"bucketType"`
	FileLockConfiguration struct {
		Value struct {
			IsFileLockEnabled bool `json:"isFileLockEnabled"`
			DefaultRetention  struct {
				Mode   string `json:"mode"`
				Period struct {
					Duration int    `json:"duration"`
					Unit     string `json:"unit"`
				} `json:"period"`
			} `json:"defaultRetention"`
		} `json:"value"`
	} `json:"fileLockConfiguration"`
}

// CreateLockedBucket creates a private bucket with Object Lock enabled and
// compliance-mode default retention of RetentionDays.
func (c *Client) CreateLockedBucket(ctx context.Context, name string) (*Bucket, error) {
	var bkt Bucket
	err := c.call(ctx, "b2_create_bucket", map[string]any{
		"accountId":       c.AccountID(),
		"bucketName":      name,
		"bucketType":      "allPrivate",
		"fileLockEnabled": true,
		"defaultRetention": map[string]any{
			"mode":   "compliance",
			"period": map[string]any{"duration": RetentionDays, "unit": "days"},
		},
	}, &bkt)
	if err != nil {
		return nil, err
	}
	return &bkt, nil
}

type Key struct {
	KeyID        string   `json:"applicationKeyId"`
	Key          string   `json:"applicationKey"`
	Name         string   `json:"keyName"`
	Capabilities []string `json:"capabilities"`
}

// ApplianceCapabilities: everything restic needs, minus any delete. The
// absence of deleteFiles/deleteBuckets is the immutability invariant.
var ApplianceCapabilities = []string{"listBuckets", "listFiles", "readFiles", "writeFiles"}

// RestoreCapabilities: read-only, printed on the Recovery Kit.
var RestoreCapabilities = []string{"listBuckets", "listFiles", "readFiles"}

// FilerCapabilities: the Filer module writes documents but can never
// delete them (BK-5a).
var FilerCapabilities = []string{"listBuckets", "listFiles", "readFiles", "writeFiles"}

// CreateKey mints a bucket-scoped application key.
func (c *Client) CreateKey(ctx context.Context, name, bucketID string, capabilities []string) (*Key, error) {
	for _, cap := range capabilities {
		if strings.HasPrefix(cap, "delete") {
			return nil, fmt.Errorf("b2: refusing to mint a key with %s — no appliance-held key may delete (D-3)", cap)
		}
	}
	var k Key
	err := c.call(ctx, "b2_create_key", map[string]any{
		"accountId":    c.AccountID(),
		"keyName":      name,
		"capabilities": capabilities,
		"bucketId":     bucketID,
	}, &k)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// ProtectFilerBucket retrofits BK-5a onto an EXISTING bucket (installs
// that predate backup): Object Lock default retention on document
// objects plus a lifecycle rule expiring noncurrent versions. New
// installs use ProvisionFilerBucket instead.
func (c *Client) ProtectFilerBucket(ctx context.Context, bucketID string, noncurrentDays int) error {
	return c.call(ctx, "b2_update_bucket", map[string]any{
		"accountId": c.AccountID(),
		"bucketId":  bucketID,
		"defaultRetention": map[string]any{
			"mode":   "compliance",
			"period": map[string]any{"duration": RetentionDays, "unit": "days"},
		},
		"lifecycleRules": []map[string]any{{
			"fileNamePrefix":           "",
			"daysFromHidingToDeleting": noncurrentDays,
		}},
	}, nil)
}

// SetLifecycle sets only the noncurrent-version expiry rule.
func (c *Client) SetLifecycle(ctx context.Context, bucketID string, noncurrentDays int) error {
	return c.call(ctx, "b2_update_bucket", map[string]any{
		"accountId": c.AccountID(),
		"bucketId":  bucketID,
		"lifecycleRules": []map[string]any{{
			"fileNamePrefix":           "",
			"daysFromHidingToDeleting": noncurrentDays,
		}},
	}, nil)
}

func (c *Client) ListBuckets(ctx context.Context) ([]Bucket, error) {
	var out struct {
		Buckets []Bucket `json:"buckets"`
	}
	if err := c.call(ctx, "b2_list_buckets", map[string]any{"accountId": c.AccountID()}, &out); err != nil {
		return nil, err
	}
	return out.Buckets, nil
}

// Health classifies account state for BK-11 — a suspended account (an
// expired card, usually) must surface differently from an upload failure.
type Health struct {
	OK bool
	// Problem is one of: "", "suspended", "unauthorized", "unreachable".
	Problem string
	Detail  string
}

func (c *Client) CheckHealth(ctx context.Context, keyID, key string) Health {
	err := c.Authorize(ctx, keyID, key)
	if err == nil {
		if _, err = c.ListBuckets(ctx); err == nil {
			return Health{OK: true}
		}
	}
	var ae *apiErr
	if ok := asAPIErr(err, &ae); ok {
		code := strings.ToLower(ae.Code + " " + ae.Message)
		switch {
		case strings.Contains(code, "suspend") || strings.Contains(code, "delinquent") ||
			strings.Contains(code, "cap") || strings.Contains(code, "payment") || ae.Status == 403:
			return Health{Problem: "suspended", Detail: ae.Message}
		case ae.Status == 401:
			return Health{Problem: "unauthorized", Detail: ae.Message}
		}
		return Health{Problem: "unreachable", Detail: ae.Error()}
	}
	return Health{Problem: "unreachable", Detail: err.Error()}
}

func asAPIErr(err error, target **apiErr) bool {
	for err != nil {
		if ae, ok := err.(*apiErr); ok {
			*target = ae
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
