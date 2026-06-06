import { render, screen } from "@testing-library/react"
import { describe, it, expect, vi, beforeEach } from "vitest"
import App from "./App.tsx"

function resolveUrl(arg: Request | string): string {
  return arg instanceof Request ? arg.url : String(arg)
}

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((arg: Request | string) => {
      const url = resolveUrl(arg)
      if (url.includes("/api/clusters")) {
        return Promise.resolve(
          new Response(JSON.stringify(["prod", "staging"]), { status: 200 }),
        )
      }
      if (url.includes("/api/snapshot")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({ items: [], total: 0, offset: 0, limit: 50 }),
            { status: 200 },
          ),
        )
      }
      return Promise.resolve(new Response(JSON.stringify([]), { status: 200 }))
    }),
  )
})

describe("App", () => {
  it("renders without crashing", async () => {
    render(<App />)
    expect(screen.getByPlaceholderText("kind")).toBeTruthy()
    expect(screen.getByPlaceholderText("CEL expression…")).toBeTruthy()
  })
})
