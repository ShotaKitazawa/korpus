package oidc

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
)

type Middleware struct {
	verifier         *gooidc.IDTokenVerifier
	resourceMetadata string // RFC 9728 §5.1: URL of the protected resource metadata document
}

// New creates a Middleware that validates JWT access tokens against the given
// OIDC issuer's JWKS. audience is matched against the token's "aud" claim.
// resourceMetadata is the full URL of the /.well-known/oauth-protected-resource document
// advertised in WWW-Authenticate challenges per RFC 9728 §5.1.
func New(ctx context.Context, issuer, audience, resourceMetadata string) (*Middleware, error) {
	provider, err := gooidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	verifier := provider.Verifier(&gooidc.Config{ClientID: audience})
	return &Middleware{verifier: verifier, resourceMetadata: resourceMetadata}, nil
}

func (m *Middleware) wwwAuthenticate(extra string) string {
	base := `Bearer realm="korpus"`
	if m.resourceMetadata != "" {
		base += fmt.Sprintf(`, resource_metadata="%s"`, m.resourceMetadata)
	}
	if extra != "" {
		base += ", " + extra
	}
	return base
}

// Handler wraps next, requiring a valid Bearer token on every request.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			w.Header().Set("WWW-Authenticate", m.wwwAuthenticate(""))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if _, err := m.verifier.Verify(r.Context(), token); err != nil {
			w.Header().Set("WWW-Authenticate", m.wwwAuthenticate(`error="invalid_token"`))
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
