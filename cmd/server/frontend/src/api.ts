import createClient from "openapi-fetch"
import type { paths, components } from "./gen/api"
import { getStoredToken } from "./auth"

// Use window.location.origin so absolute URLs are always constructed —
// Node.js (undici) rejects relative URLs in new Request(), and it also
// means vi.stubGlobal("fetch", ...) in tests takes effect at call time.
export const api = createClient<paths>({
  baseUrl:
    typeof window !== "undefined" ? window.location.origin : "http://localhost",
  fetch: (...args: Parameters<typeof globalThis.fetch>) => {
    const token = getStoredToken()
    if (token) {
      const [input, init = {}] = args
      const headers = new Headers((init as RequestInit).headers)
      headers.set("Authorization", `Bearer ${token}`)
      return globalThis.fetch(input, { ...init as RequestInit, headers })
    }
    return globalThis.fetch(...args)
  },
})
export type { components }
export type ResourceMeta = components["schemas"]["ResourceMeta"]
export type HistoryEntry = components["schemas"]["HistoryEntry"]
export type DiffResult = components["schemas"]["DiffResult"]
export type ResourceListPage = components["schemas"]["ResourceListPage"]
export type ChurnEntry = components["schemas"]["ChurnEntry"]
