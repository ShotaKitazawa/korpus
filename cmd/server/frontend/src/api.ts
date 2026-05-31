import createClient from "openapi-fetch"
import type { paths, components } from "./gen/api"

// Use window.location.origin so absolute URLs are always constructed —
// Node.js (undici) rejects relative URLs in new Request(), and it also
// means vi.stubGlobal("fetch", ...) in tests takes effect at call time.
export const api = createClient<paths>({
  baseUrl:
    typeof window !== "undefined" ? window.location.origin : "http://localhost",
  fetch: (...args: Parameters<typeof globalThis.fetch>) =>
    globalThis.fetch(...args),
})
export type { components }
export type ResourceMeta = components["schemas"]["ResourceMeta"]
export type HistoryEntry = components["schemas"]["HistoryEntry"]
export type DiffResult = components["schemas"]["DiffResult"]
