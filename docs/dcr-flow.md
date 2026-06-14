# OAuth 2.0 DCR Flow

When OIDC is enabled, korpus acts as a proxy between MCP clients and the upstream IdP to support [Dynamic Client Registration (RFC 7591)](https://datatracker.ietf.org/doc/html/rfc7591). This proxy layer exists because some IdPs do not automatically include an audience (`aud`) claim in issued access tokens — the client must explicitly request the audience in each authorization request. korpus injects this parameter transparently so MCP clients work without any extra configuration.

## Sequence

```mermaid
sequenceDiagram
    participant C as MCP Client
    participant K as korpus
    participant I as IdP

    rect rgb(240, 240, 240)
        Note over C,K: Phase 0: Protected Resource Discovery
        C->>K: GET /mcp (no token)
        K->>C: 401 WWW-Authenticate: resource_metadata=".../oauth-protected-resource/mcp"
        C->>K: GET /.well-known/oauth-protected-resource/mcp
        K->>C: { resource: "https://host/mcp", authorization_servers: ["https://host"] }
    end

    rect rgb(240, 240, 240)
        Note over C,I: Phase 1: AS Metadata Discovery
        C->>K: GET /.well-known/oauth-authorization-server
        K->>I: GET /.well-known/openid-configuration
        I->>K: { authorization_endpoint, registration_endpoint, ... }
        Note over K: rewrite authorization_endpoint → /oauth2/auth<br/>rewrite registration_endpoint → /oauth2/register
        K->>C: { authorization_endpoint: "https://host/oauth2/auth", registration_endpoint: "https://host/oauth2/register", ... }
    end

    rect rgb(240, 240, 240)
        Note over C,I: Phase 2: DCR
        C->>K: POST /oauth2/register { client_name, redirect_uris, ... }
        Note over K: inject audience: ["https://host/mcp"]
        K->>I: POST <upstream registration_endpoint> { ..., audience: ["https://host/mcp"] }
        I->>K: { client_id, client_secret }
        K->>C: { client_id, client_secret }
    end

    rect rgb(240, 240, 240)
        Note over C,I: Phase 3: Authorization Code Flow (PKCE)
        C->>K: GET /oauth2/auth?response_type=code&client_id=...&code_challenge=...&state=...
        Note over K: inject &audience=https://host/mcp
        K->>C: 302 https://idp/oauth2/auth?...&audience=https://host/mcp
        C->>I: (browser) GET https://idp/oauth2/auth?...
        I->>C: (browser) 302 <redirect_uri>?code=...&state=...
    end

    rect rgb(240, 240, 240)
        Note over C,I: Phase 4: Token Exchange
        C->>I: POST /oauth2/token { code, code_verifier }
        I->>C: { access_token: JWT { aud: ["https://host/mcp"], ... } }
    end

    rect rgb(240, 240, 240)
        Note over C,K: Phase 5: API Access
        C->>K: GET /mcp  Authorization: Bearer <token>
        Note over K: verify: signature + aud + exp
        K->>C: 200 (MCP response)
    end
```

## What korpus proxies

| Endpoint | Role |
|---|---|
| `GET /.well-known/oauth-protected-resource/mcp` | RFC 9728 resource metadata; points clients at korpus as the AS |
| `GET /.well-known/oauth-authorization-server` | RFC 8414 AS metadata (proxied from IdP, endpoints rewritten) |
| `GET /.well-known/openid-configuration` | OIDC discovery (same document, same rewrites) |
| `POST /oauth2/register` | DCR proxy; injects `audience` into the registration body |
| `GET /oauth2/auth` | Authorization endpoint proxy; injects `audience` query parameter |

Token exchange (`POST /oauth2/token`) and JWKS (`GET /.well-known/jwks.json`) go directly to the IdP — korpus does not intercept them.
