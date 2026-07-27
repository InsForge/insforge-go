package insforge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/InsForge/insforge-go/insforge"
)

func newTestClient(t *testing.T, mux *http.ServeMux) *insforge.Client {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return insforge.NewClient(insforge.Config{BaseURL: srv.URL, AnonKey: "test-key"})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func TestSignUp_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		writeJSON(w, 201, map[string]interface{}{"id": "user-1", "email": "a@b.com"})
	})

	client := newTestClient(t, mux)
	result := client.Auth.SignUp(context.Background(), "a@b.com", "secret")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Data["id"] != "user-1" {
		t.Fatalf("expected user-1, got %v", result.Data["id"])
	}
}

func TestSignUp_Error(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/users", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 400, map[string]interface{}{
			"error": map[string]interface{}{"message": "Email taken", "code": "EMAIL_EXISTS"},
		})
	})

	client := newTestClient(t, mux)
	result := client.Auth.SignUp(context.Background(), "a@b.com", "secret")
	if result.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if result.Error.Message != "Email taken" {
		t.Fatalf("expected 'Email taken', got %q", result.Error.Message)
	}
}

func TestSignIn_StoresToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/sessions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]interface{}{
			"accessToken":  "jwt-abc",
			"refreshToken": "refresh-xyz",
			"user":         map[string]interface{}{"id": "u1", "email": "a@b.com"},
		})
	})

	client := newTestClient(t, mux)
	result := client.Auth.SignInWithPassword(context.Background(), "a@b.com", "secret")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Data.AccessToken != "jwt-abc" {
		t.Fatalf("expected jwt-abc, got %s", result.Data.AccessToken)
	}
}

func TestSignInWithOtp_PostsEmailToSendOtpEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/email/send-otp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}
		if body["email"] != "user@example.com" {
			t.Fatalf("expected email user@example.com, got %v", body["email"])
		}
		writeJSON(w, 202, map[string]interface{}{
			"success": true,
			"message": "If sign-in is available for this email, we have sent a verification code.",
		})
	})

	client := newTestClient(t, mux)
	result := client.Auth.SignInWithOtp(context.Background(), "user@example.com")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Data["success"] != true {
		t.Fatalf("expected success=true, got %v", result.Data["success"])
	}
}

func TestVerifyOtp_CreatesSessionAndStoresToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}
		if body["method"] != "otp" {
			t.Fatalf("expected method otp, got %v", body["method"])
		}
		if body["email"] != "user@example.com" || body["otp"] != "123456" {
			t.Fatalf("unexpected credentials: %v", body)
		}
		if body["name"] != "Ada Lovelace" {
			t.Fatalf("expected name Ada Lovelace, got %v", body["name"])
		}
		writeJSON(w, 200, map[string]interface{}{
			"accessToken":  "jwt-otp",
			"refreshToken": "refresh-otp",
			"user":         map[string]interface{}{"id": "u1", "email": "user@example.com", "emailVerified": true},
		})
	})

	client := newTestClient(t, mux)
	result := client.Auth.VerifyOtp(context.Background(), "user@example.com", "123456", "Ada Lovelace")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Data.AccessToken != "jwt-otp" {
		t.Fatalf("expected jwt-otp, got %s", result.Data.AccessToken)
	}
}

func TestVerifyOtp_OmitsEmptyName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/sessions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}
		if _, present := body["name"]; present {
			t.Fatalf("expected name to be omitted, got %v", body["name"])
		}
		writeJSON(w, 200, map[string]interface{}{
			"accessToken": "jwt-otp",
			"user":        map[string]interface{}{"id": "u1", "email": "user@example.com"},
		})
	})

	client := newTestClient(t, mux)
	result := client.Auth.VerifyOtp(context.Background(), "user@example.com", "123456", "")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
}

func TestVerifyOtp_Error(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/sessions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 401, map[string]interface{}{
			"error": map[string]interface{}{"message": "Invalid or expired code", "code": "INVALID_OTP"},
		})
	})

	client := newTestClient(t, mux)
	result := client.Auth.VerifyOtp(context.Background(), "user@example.com", "000000", "")
	if result.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if result.Error.Message != "Invalid or expired code" {
		t.Fatalf("expected 'Invalid or expired code', got %q", result.Error.Message)
	}
}

func TestSignOut_ClearsToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]interface{}{"message": "ok"})
	})

	client := newTestClient(t, mux)
	client.SetAccessToken("jwt-abc")
	result := client.Auth.SignOut(context.Background())
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
}
