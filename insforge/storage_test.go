package insforge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestStorage_GetPublicURL(t *testing.T) {
	mux := http.NewServeMux()
	client := newTestClient(t, mux)
	url := client.Storage.From("avatars").GetPublicURL("user.png")
	if !strings.Contains(url, "/api/storage/buckets/avatars/objects/user.png") {
		t.Fatalf("unexpected URL: %s", url)
	}
}

func TestStorage_List(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/storage/buckets/avatars/objects", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]interface{}{"objects": []map[string]interface{}{{"key": "a.png"}}})
	})
	client := newTestClient(t, mux)
	result := client.Storage.From("avatars").List(context.Background(), nil)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
}

// newUploadMux returns a mux that serves the upload-strategy and direct PUT
// routes for bucket "docs", recording each requested filename/key.
func newUploadMux(t *testing.T, requestedKeys *[]string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/storage/buckets/docs/upload-strategy", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST strategy request, got %s", r.Method)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode strategy body: %v", err)
		}
		if _, has := body["upsert"]; has {
			t.Fatal("strategy request must not carry an upsert flag")
		}
		if _, has := body["autoKey"]; has {
			t.Fatal("strategy request must not carry an autoKey flag; keys are minted client-side")
		}
		filename, _ := body["filename"].(string)
		*requestedKeys = append(*requestedKeys, filename)
		writeJSON(w, 200, map[string]interface{}{
			"method":          "direct",
			"uploadUrl":       "/api/storage/buckets/docs/objects/" + filename,
			"key":             filename,
			"confirmRequired": false,
		})
	})
	mux.HandleFunc("/api/storage/buckets/docs/objects/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("expected direct upload via PUT, got %s", r.Method)
		}
		key := strings.TrimPrefix(r.URL.Path, "/api/storage/buckets/docs/objects/")
		writeJSON(w, 200, map[string]interface{}{"bucket": "docs", "key": key})
	})
	return mux
}

func TestStorage_Upload_DirectPutToExactKey(t *testing.T) {
	var keys []string
	mux := newUploadMux(t, &keys)
	client := newTestClient(t, mux)

	result := client.Storage.From("docs").Upload(context.Background(), "report.pdf", []byte("abc"), "application/pdf")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if len(keys) != 1 || keys[0] != "report.pdf" {
		t.Fatalf("expected upload to the exact key %q, got %v", "report.pdf", keys)
	}
}

func TestStorage_UploadAuto_MintsUniqueClientSideKeys(t *testing.T) {
	var keys []string
	mux := newUploadMux(t, &keys)
	client := newTestClient(t, mux)
	bucket := client.Storage.From("docs")

	for i := 0; i < 2; i++ {
		result := bucket.UploadAuto(context.Background(), "report.pdf", []byte("abc"), "application/pdf")
		if result.Error != nil {
			t.Fatalf("unexpected error on upload %d: %v", i, result.Error)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 strategy requests, got %d", len(keys))
	}
	// Client-generated key: sanitized base + timestamp + random, preserving ext.
	pattern := regexp.MustCompile(`^report-\d+-[a-z0-9]+\.pdf$`)
	for _, key := range keys {
		if !pattern.MatchString(key) {
			t.Fatalf("key %q does not match auto-key pattern %s", key, pattern)
		}
	}
	if keys[0] == keys[1] {
		t.Fatalf("repeated UploadAuto calls must mint distinct keys, both were %q", keys[0])
	}
}

func TestStorage_UploadAuto_SanitizesFilename(t *testing.T) {
	var keys []string
	mux := newUploadMux(t, &keys)
	client := newTestClient(t, mux)

	result := client.Storage.From("docs").UploadAuto(context.Background(), "my doc (1).png", []byte("abc"), "image/png")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if pattern := regexp.MustCompile(`^my-doc--1--\d+-[a-z0-9]+\.png$`); !pattern.MatchString(keys[0]) {
		t.Fatalf("key %q does not match sanitized pattern %s", keys[0], pattern)
	}
}

func TestStorage_UploadAuto_EmptyFilenameFallsBackToFileBase(t *testing.T) {
	var keys []string
	mux := newUploadMux(t, &keys)
	client := newTestClient(t, mux)

	result := client.Storage.From("docs").UploadAutoReader(context.Background(), "", strings.NewReader("abc"), "")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if pattern := regexp.MustCompile(`^file-\d+-[a-z0-9]+$`); !pattern.MatchString(keys[0]) {
		t.Fatalf("key %q does not match fallback pattern %s", keys[0], pattern)
	}
}

func TestStorage_Remove(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/storage/buckets/avatars/objects/a.png", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("expected DELETE, got %s", r.Method)
		}
		writeJSON(w, 200, map[string]interface{}{"message": "deleted"})
	})
	client := newTestClient(t, mux)
	result := client.Storage.From("avatars").Remove(context.Background(), "a.png")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
}
