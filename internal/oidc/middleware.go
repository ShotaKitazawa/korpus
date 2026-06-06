package oidc

import (
	"context"
	"net/http"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
)

type Middleware struct {
	verifier *gooidc.IDTokenVerifier
}

// New creates a Middleware that validates JWT access tokens against the given
// OIDC issuer's JWKS. audience is matched against the token's "aud" claim.
func New(ctx context.Context, issuer, audience string) (*Middleware, error) {
	provider, err := gooidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	verifier := provider.Verifier(&gooidc.Config{ClientID: audience})
	return &Middleware{verifier: verifier}, nil
}

// Handler wraps next, requiring a valid Bearer token on every request.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="korpus"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if _, err := m.verifier.Verify(r.Context(), token); err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="korpus" error="invalid_token"`)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
