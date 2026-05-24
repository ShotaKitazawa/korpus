import { render, screen } from "@testing-library/react"
import { describe, it, expect, vi, beforeEach } from "vitest"
import App from "./App.tsx"

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string) => {
      if (url.startsWith("/api/clusters")) {
        return Promise.resolve({
          json: () => Promise.resolve(["prod", "staging"]),
          text: () => Promise.resolve(""),
        })
      }
      return Promise.resolve({
        json: () => Promise.resolve([]),
        text: () => Promise.resolve(""),
      })
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
