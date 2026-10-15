package b2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeB2 is a minimal B2 API for provisioning tests.
func fakeB2(t *testing.T, mutate func(op string, body map[string]any) (int, any)) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if op == "b2_authorize_account" {
			json.NewEncoder(w).Encode(map[string]any{
				"accountId": "acct1", "authorizationToken": "tok", "apiUrl": srv.URL,
			})
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if mutate != nil {
			if code, resp := mutate(op, body); resp != nil {
				w.WriteHeader(code)
				json.NewEncoder(w).Encode(resp)
				return
			}
		}
		switch op {
		case "b2_create_bucket":
			json.NewEncoder(w).Encode(map[string]any{
				"bucketId": "bkt1", "bucketName": body["bucketName"], "bucketType": "allPrivate",
				"fileLockConfiguration": map[string]any{"value": map[string]any{
					"isFileLockEnabled": true,
					"defaultRetention": map[string]any{"mode": "compliance",
						"period": map[string]any{"duration": 90, "unit": "days"}},
				}},
			})
		case "b2_create_key":
			json.NewEncoder(w).Encode(map[string]any{
				"applicationKeyId": "kid-" + body["keyName"].(string),
				"applicationKey":   "K-secret",
				"keyName":          body["keyName"],
				"capabilities":     body["capabilities"],
			})
		case "b2_list_buckets":
			json.NewEncoder(w).Encode(map[string]any{"buckets": []any{}})
		case "b2_update_bucket":
			json.NewEncoder(w).Encode(map[string]any{})
		default:
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]any{"status": 400, "code": "bad_request", "message": "unknown op " + op})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProvisionHappyPath(t *testing.T) {
	srv := fakeB2(t, nil)
	c := New(srv.URL)
	p, err := Provision(context.Background(), c, "master-id", "master-key", "Example CPA, LLP")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Bucket.Name, "vibe-backup-example-cpa-llp-") {
		t.Fatalf("bucket name: %v", p.Bucket.Name)
	}
	for _, cap := range p.ApplianceKey.Capabilities {
		if strings.HasPrefix(cap, "delete") {
			t.Fatalf("appliance key can delete: %v", p.ApplianceKey.Capabilities)
		}
	}
	if len(p.RestoreKey.Capabilities) != 3 {
		t.Fatalf("restore key: %v", p.RestoreKey.Capabilities)
	}
}

func TestProvisionRefusesUnlockedBucket(t *testing.T) {
	srv := fakeB2(t, func(op string, body map[string]any) (int, any) {
		if op == "b2_create_bucket" {
			return 200, map[string]any{"bucketId": "bkt1", "bucketName": body["bucketName"],
				"fileLockConfiguration": map[string]any{"value": map[string]any{"isFileLockEnabled": false}}}
		}
		return 0, nil
	})
	c := New(srv.URL)
	if _, err := Provision(context.Background(), c, "id", "key", "Firm"); err == nil || !strings.Contains(err.Error(), "Object Lock DISABLED") {
		t.Fatalf("must refuse unlocked bucket, got %v", err)
	}
}

func TestProvisionRefusesGrantedDelete(t *testing.T) {
	srv := fakeB2(t, func(op string, body map[string]any) (int, any) {
		if op == "b2_create_key" {
			return 200, map[string]any{"applicationKeyId": "kid", "applicationKey": "K",
				"keyName": body["keyName"], "capabilities": []string{"writeFiles", "deleteFiles"}}
		}
		return 0, nil
	})
	c := New(srv.URL)
	if _, err := Provision(context.Background(), c, "id", "key", "Firm"); err == nil || !strings.Contains(err.Error(), "deleteFiles") {
		t.Fatalf("must refuse a key granted delete, got %v", err)
	}
}

func TestCreateKeyRefusesDeleteRequest(t *testing.T) {
	c := New("http://unused")
	c.auth = &authResponse{AccountID: "a", APIURL: "http://unused", AuthorizationToken: "t"}
	if _, err := c.CreateKey(context.Background(), "bad", "bkt", []string{"deleteFiles"}); err == nil {
		t.Fatal("must refuse to mint a deleting key")
	}
}

func TestHealthClassification(t *testing.T) {
	cases := []struct {
		status  int
		code    string
		problem string
	}{
		{401, "unauthorized", "unauthorized"},
		{403, "transaction_cap_exceeded", "suspended"},
		{403, "account_suspended", "suspended"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			json.NewEncoder(w).Encode(map[string]any{"status": tc.status, "code": tc.code, "message": tc.code})
		}))
		h := New(srv.URL).CheckHealth(context.Background(), "id", "key")
		srv.Close()
		if h.OK || h.Problem != tc.problem {
			t.Fatalf("%s: want %s, got %+v", tc.code, tc.problem, h)
		}
	}
}

func TestHealthOK(t *testing.T) {
	srv := fakeB2(t, nil)
	h := New(srv.URL).CheckHealth(context.Background(), "id", "key")
	if !h.OK {
		t.Fatalf("want healthy, got %+v", h)
	}
}

func TestVerifyCannotDelete(t *testing.T) {
	srv := fakeB2(t, func(op string, _ map[string]any) (int, any) {
		if op == "b2_delete_file_version" {
			return 401, map[string]any{"status": 401, "code": "unauthorized", "message": "no capability"}
		}
		return 0, nil
	})
	if err := VerifyCannotDelete(context.Background(), srv.URL, "id", "key"); err != nil {
		t.Fatal(err)
	}

	srvBad := fakeB2(t, func(op string, _ map[string]any) (int, any) {
		if op == "b2_delete_file_version" {
			return 200, map[string]any{}
		}
		return 0, nil
	})
	if err := VerifyCannotDelete(context.Background(), srvBad.URL, "id", "key"); err == nil || !strings.Contains(err.Error(), "DELETE SUCCEEDED") {
		t.Fatalf("a successful delete must be a hard failure, got %v", err)
	}
}

func fakeAuthorized(t *testing.T, caps []string, lock bool) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		switch op {
		case "b2_authorize_account":
			json.NewEncoder(w).Encode(map[string]any{
				"accountId": "acct1", "authorizationToken": "tok", "apiUrl": srv.URL,
				"allowed": map[string]any{"capabilities": caps, "bucketId": "bkt1", "bucketName": "firm-bkt"},
			})
		case "b2_list_buckets":
			json.NewEncoder(w).Encode(map[string]any{"buckets": []any{map[string]any{
				"bucketId": "bkt1", "bucketName": "firm-bkt",
				"fileLockConfiguration": map[string]any{"value": map[string]any{"isFileLockEnabled": lock}},
			}}})
		default:
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUseExistingHappyPath(t *testing.T) {
	srv := fakeAuthorized(t, []string{"listBuckets", "listFiles", "readFiles", "writeFiles"}, true)
	p, err := UseExisting(context.Background(), New(srv.URL), "kid", "ksecret", "firm-bkt", false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Bucket.Name != "firm-bkt" || p.ApplianceKey.KeyID != "kid" || p.RestoreKey.Key != "ksecret" {
		t.Fatalf("unexpected: %+v", p)
	}
}

func TestUseExistingRefusals(t *testing.T) {
	ok := []string{"listBuckets", "listFiles", "readFiles", "writeFiles"}
	cases := map[string]struct {
		caps []string
		lock bool
		want string
	}{
		"delete":   {append([]string{"deleteFiles"}, ok...), true, "deleteFiles"},
		"nowrite":  {[]string{"listFiles", "readFiles"}, true, "writeFiles"},
		"unlocked": {ok, false, "Object Lock DISABLED"},
	}
	for name, c := range cases {
		srv := fakeAuthorized(t, c.caps, c.lock)
		if _, err := UseExisting(context.Background(), New(srv.URL), "kid", "k", "firm-bkt", false); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: want %q, got %v", name, c.want, err)
		}
	}
}

func TestUseExistingDeleteOverride(t *testing.T) {
	srv := fakeAuthorized(t, []string{"deleteFiles", "listBuckets", "listFiles", "readFiles", "writeFiles"}, true)
	p, err := UseExisting(context.Background(), New(srv.URL), "kid", "k", "firm-bkt", true)
	if err != nil {
		t.Fatal(err)
	}
	if !p.DeleteCapable {
		t.Fatal("override must be recorded so the status card can warn")
	}
}
