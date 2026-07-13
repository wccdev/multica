package gitea

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_GetAuthenticatedUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/user" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "token test-pat" {
			t.Errorf("Authorization header = %q, want %q", got, "token test-pat")
		}
		_ = json.NewEncoder(w).Encode(AuthenticatedUser{Login: "octocat", AvatarURL: "https://example.com/a.png"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-pat", nil)
	user, err := c.GetAuthenticatedUser(context.Background())
	if err != nil {
		t.Fatalf("GetAuthenticatedUser: %v", err)
	}
	if user.Login != "octocat" {
		t.Errorf("Login = %q, want octocat", user.Login)
	}
}

func TestClient_GetAuthenticatedUser_InvalidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "bad-pat", nil)
	if _, err := c.GetAuthenticatedUser(context.Background()); err == nil {
		t.Error("expected error for 401 response")
	}
}

func TestClient_CreateAndDeleteWebhook(t *testing.T) {
	var created bool
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/acme/widget/hooks":
			created = true
			var body createWebhookRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			if body.Config.Secret != "shh" {
				t.Errorf("secret = %q, want shh", body.Config.Secret)
			}
			_ = json.NewEncoder(w).Encode(webhookResponse{ID: 42})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/repos/acme/widget/hooks/42":
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-pat", nil)
	hookID, err := c.CreateWebhook(context.Background(), "acme", "widget", "https://backend.example.com/api/webhooks/gitea/ws-1", "shh")
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if hookID != 42 {
		t.Errorf("hookID = %d, want 42", hookID)
	}
	if !created {
		t.Error("expected create request to be sent")
	}

	if err := c.DeleteWebhook(context.Background(), "acme", "widget", 42); err != nil {
		t.Fatalf("DeleteWebhook: %v", err)
	}
	if !deleted {
		t.Error("expected delete request to be sent")
	}
}

func TestClient_DeleteWebhook_404IsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-pat", nil)
	if err := c.DeleteWebhook(context.Background(), "acme", "widget", 99); err != nil {
		t.Errorf("expected 404 to be treated as success, got %v", err)
	}
}
