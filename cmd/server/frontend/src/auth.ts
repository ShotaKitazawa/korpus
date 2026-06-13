export type AuthConfig =
  | { enabled: false }
  | {
      enabled: true;
      issuer: string;
      clientId: string;
      audience: string;
    };

const TOKEN_KEY = "korpus_access_token";
const TOKEN_EXPIRY_KEY = "korpus_token_expiry";
const VERIFIER_KEY = "korpus_pkce_verifier";
const STATE_KEY = "korpus_oauth_state";

function randomBase64url(byteCount: number): string {
  const bytes = crypto.getRandomValues(new Uint8Array(byteCount));
  return btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=/g, "");
}

async function sha256base64url(plain: string): Promise<string> {
  const buf = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(plain));
  return btoa(String.fromCharCode(...new Uint8Array(buf)))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=/g, "");
}

export function getStoredToken(): string | null {
  const token = sessionStorage.getItem(TOKEN_KEY);
  const expiry = sessionStorage.getItem(TOKEN_EXPIRY_KEY);
  if (!token || !expiry) return null;
  if (Date.now() > parseInt(expiry, 10)) {
    sessionStorage.removeItem(TOKEN_KEY);
    sessionStorage.removeItem(TOKEN_EXPIRY_KEY);
    return null;
  }
  return token;
}

type OIDCEndpoints = {
  authorizationEndpoint: string;
  tokenEndpoint: string;
};

async function discoverEndpoints(issuer: string): Promise<OIDCEndpoints> {
  const base = issuer.endsWith("/") ? issuer.slice(0, -1) : issuer;
  const res = await fetch(`${base}/.well-known/openid-configuration`);
  if (!res.ok) throw new Error(`OIDC discovery failed: ${res.status}`);
  const doc = (await res.json()) as {
    authorization_endpoint: string;
    token_endpoint: string;
  };
  if (!doc.authorization_endpoint || !doc.token_endpoint) {
    throw new Error("OIDC discovery document missing required endpoint fields");
  }
  return {
    authorizationEndpoint: doc.authorization_endpoint,
    tokenEndpoint: doc.token_endpoint,
  };
}

export async function initAuth(): Promise<void> {
  const res = await fetch("/auth-config");
  const cfg: AuthConfig = await res.json();
  if (!cfg.enabled) return;

  const endpoints = await discoverEndpoints(cfg.issuer);

  const params = new URLSearchParams(window.location.search);
  const code = params.get("code");
  const state = params.get("state");

  if (code && state) {
    const savedState = sessionStorage.getItem(STATE_KEY);
    const verifier = sessionStorage.getItem(VERIFIER_KEY);
    if (state === savedState && verifier) {
      await exchangeCode(cfg, endpoints, code, verifier);
      sessionStorage.removeItem(STATE_KEY);
      sessionStorage.removeItem(VERIFIER_KEY);
      params.delete("code");
      params.delete("state");
      const newSearch = params.toString() ? "?" + params.toString() : "";
      history.replaceState(null, "", window.location.pathname + newSearch);
    }
  }

  if (!getStoredToken()) {
    await startPKCEFlow(cfg, endpoints);
  }
}

async function startPKCEFlow(
  cfg: AuthConfig & { enabled: true },
  endpoints: OIDCEndpoints,
): Promise<void> {
  const verifier = randomBase64url(32);
  const challenge = await sha256base64url(verifier);
  const state = randomBase64url(16);

  sessionStorage.setItem(VERIFIER_KEY, verifier);
  sessionStorage.setItem(STATE_KEY, state);

  const authParams = new URLSearchParams({
    response_type: "code",
    client_id: cfg.clientId,
    redirect_uri: window.location.origin + "/",
    scope: "openid",
    audience: cfg.audience,
    code_challenge: challenge,
    code_challenge_method: "S256",
    state,
  });

  window.location.href = `${endpoints.authorizationEndpoint}?${authParams}`;
  // redirect — never returns
  await new Promise<never>(() => {});
}

async function exchangeCode(
  cfg: AuthConfig & { enabled: true },
  endpoints: OIDCEndpoints,
  code: string,
  verifier: string,
): Promise<void> {
  const res = await fetch(endpoints.tokenEndpoint, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "authorization_code",
      client_id: cfg.clientId,
      code,
      redirect_uri: window.location.origin + "/",
      code_verifier: verifier,
    }),
  });
  if (!res.ok) throw new Error(`token exchange failed: ${res.status}`);
  const { access_token, expires_in } = (await res.json()) as {
    access_token: string;
    expires_in: number;
  };
  sessionStorage.setItem(TOKEN_KEY, access_token);
  sessionStorage.setItem(TOKEN_EXPIRY_KEY, String(Date.now() + expires_in * 1000));
}
