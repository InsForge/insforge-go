package insforge_test

import (
	"context"
	"encoding/json"
	"net/http"
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

func TestStorage_RemoveMany(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/storage/buckets/docs/objects", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("expected DELETE, got %s", r.Method)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}
		keys, _ := body["keys"].([]interface{})
		if len(keys) != 3 || keys[0] != "a.pdf" || keys[1] != "missing.pdf" || keys[2] != "locked.pdf" {
			t.Fatalf("unexpected keys: %v", body["keys"])
		}
		writeJSON(w, 200, map[string]interface{}{
			"results": []map[string]interface{}{
				{"key": "a.pdf", "status": "deleted"},
				{"key": "missing.pdf", "status": "notFound"},
				{"key": "locked.pdf", "status": "failed", "message": "Delete denied"},
			},
		})
	})

	client := newTestClient(t, mux)
	result := client.Storage.From("docs").RemoveMany(context.Background(), []string{"a.pdf", "/missing.pdf", "locked.pdf"})
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if len(result.Data.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(result.Data.Results))
	}
	if result.Data.Results[1].Status != "notFound" {
		t.Fatalf("expected notFound, got %s", result.Data.Results[1].Status)
	}
	if result.Data.Results[2].Message != "Delete denied" {
		t.Fatalf("expected 'Delete denied', got %q", result.Data.Results[2].Message)
	}
}

func TestStorage_RemoveMany_Error(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/storage/buckets/docs/objects", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 400, map[string]interface{}{
			"error": map[string]interface{}{"message": "At least one object key is required", "code": "STORAGE_ERROR"},
		})
	})

	client := newTestClient(t, mux)
	result := client.Storage.From("docs").RemoveMany(context.Background(), []string{})
	if result.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if result.Error.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", result.Error.StatusCode)
	}
	if result.Error.Message != "At least one object key is required" {
		t.Fatalf("unexpected message: %q", result.Error.Message)
	}
}
