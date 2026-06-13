package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResourceBaseURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://korpus.kanatakita.com/mcp", "https://korpus.kanatakita.com"},
		{"https://example.com", "https://example.com"},
		{"https://example.com/a/b/c", "https://example.com"},
		{"not-a-url", "not-a-url"},
	}
	for _, c := range cases {
		if got := resourceBaseURL(c.in); got != c.want {
			t.Errorf("resourceBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMergeAudience_InjectsIntoEmpty(t *testing.T) {
	body := `{"client_id":"abc"}`
	got := mergeAudience([]byte(body), "https://api.example.com")
	if !strings.Contains(string(got), `"https://api.example.com"`) {
		t.Fatalf("audience not injected: %s", got)
	}
}

func TestMergeAudience_MergesWithExisting(t *testing.T) {
	body := `{"audience":["https://existing.example.com"]}`
	got := mergeAudience([]byte(body), "https://api.example.com")
	s := string(got)
	if !strings.Contains(s, `"https://existing.example.com"`) {
		t.Fatalf("existing audience lost: %s", s)
	}
	if !strings.Contains(s, `"https://api.example.com"`) {
		t.Fatalf("injected audience missing: %s", s)
	}
}

func TestMergeAudience_Deduplicates(t *testing.T) {
	body := `{"audience":["https://api.example.com"]}`
	got := mergeAudience([]byte(body), "https://api.example.com")
	count := strings.Count(string(got), "https://api.example.com")
	if count != 1 {
		t.Fatalf("expected 1 occurrence, got %d: %s", count, got)
	}
}

func TestMergeAudience_NonJSONPassthrough(t *testing.T) {
	body := []byte("not json")
	got := mergeAudience(body, "https://api.example.com")
	if string(got) != string(body) {
		t.Fatalf("non-JSON body should pass through unchanged")
	}
}

func TestOIDCDiscoveryProxyHandler_RewritesRegistrationEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                "https://auth.example.com",
			"authorization_endpoint": "https://auth.example.com/oauth2/auth",
			"token_endpoint":        "https://auth.example.com/oauth2/token",
			"registration_endpoint": "https://auth.example.com/oauth2/register",
		})
	}))
	defer upstream.Close()

	handler := oidcDiscoveryProxyHandler(upstream.URL, "https://app.example.com")
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest("GET", "/.well-known/openid-configuration", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var doc map[string]any
	if err := json.NewDecoder(w.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if got := doc["registration_endpoint"]; got != "https://app.example.com/oauth2/register" {
		t.Fatalf("registration_endpoint = %v, want https://app.example.com/oauth2/register", got)
	}
	if got := doc["authorization_endpoint"]; got != "https://auth.example.com/oauth2/auth" {
		t.Fatalf("authorization_endpoint should be preserved, got %v", got)
	}
}

func TestOIDCDiscoveryProxyHandler_CachesResponse(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{"issuer": "https://auth.example.com"})
	}))
	defer upstream.Close()

	handler := oidcDiscoveryProxyHandler(upstream.URL, "https://app.example.com")
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("GET", "/.well-known/openid-configuration", nil))
	}
	if calls != 1 {
		t.Fatalf("expected upstream to be called once, got %d", calls)
	}
}

func TestOIDCDiscoveryProxyHandler_UpstreamError(t *testing.T) {
	handler := oidcDiscoveryProxyHandler("http://127.0.0.1:0", "https://app.example.com")
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest("GET", "/.well-known/openid-configuration", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestOIDCRegistrationProxyHandler_InjectsAudience(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			json.NewEncoder(w).Encode(map[string]any{
				"registration_endpoint": "http://" + r.Host + "/oauth2/register",
			})
			return
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write(body)
	}))
	defer upstream.Close()

	handler := oidcRegistrationProxyHandler(upstream.URL, "https://korpus.example.com/mcp")
	body := `{"client_name":"test","redirect_uris":["http://localhost/cb"]}`
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest("POST", "/oauth2/register", strings.NewReader(body)))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	auds, _ := received["audience"].([]any)
	if len(auds) == 0 || auds[0] != "https://korpus.example.com/mcp" {
		t.Fatalf("audience not injected: %v", received["audience"])
	}
}

func TestOIDCRegistrationProxyHandler_DeduplicatesAudience(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			json.NewEncoder(w).Encode(map[string]any{
				"registration_endpoint": "http://" + r.Host + "/oauth2/register",
			})
			return
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusCreated)
		w.Write(body)
	}))
	defer upstream.Close()

	handler := oidcRegistrationProxyHandler(upstream.URL, "https://korpus.example.com/mcp")
	body := `{"audience":["https://korpus.example.com/mcp"]}`
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest("POST", "/oauth2/register", strings.NewReader(body)))

	auds, _ := received["audience"].([]any)
	count := 0
	for _, a := range auds {
		if a == "https://korpus.example.com/mcp" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected audience to appear once, got %d: %v", count, auds)
	}
}

func TestOIDCRegistrationProxyHandler_UpstreamDiscoveryError(t *testing.T) {
	handler := oidcRegistrationProxyHandler("http://127.0.0.1:0", "https://korpus.example.com/mcp")
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest("POST", "/oauth2/register", strings.NewReader("{}")))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}
