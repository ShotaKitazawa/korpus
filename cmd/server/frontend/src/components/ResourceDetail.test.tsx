import {
  render,
  screen,
  fireEvent,
  waitFor,
  cleanup,
} from "@testing-library/react"
import { describe, it, expect, vi, afterEach } from "vitest"
import ResourceDetail from "./ResourceDetail.tsx"
import type { ResourceMeta } from "../App.tsx"

const mockResource: ResourceMeta = {
  cluster: "test-cluster",
  kind: "Pod",
  name: "my-pod",
  namespace: "default",
  labels: { app: "my-app" },
}

// Quoted string values trigger green highlighting; numbers/bools/null use their own colors.
const sampleYaml = `kind: Pod
metadata:
  name: "my-pod"
  labels:
    app: "my-app"
# a comment
spec:
  replicas: 3
  enabled: true
  value: null`

const historyEntries = [
  {
    sha: "abc12345abcd",
    message: "update pod",
    timestamp: "2026-05-30T00:00:00Z",
  },
  {
    sha: "def67890efgh",
    message: "initial",
    timestamp: "2026-05-29T00:00:00Z",
  },
]

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe("ResourceDetail", () => {
  it("renders yaml tab by default", () => {
    render(<ResourceDetail resource={mockResource} yaml={sampleYaml} />)
    expect(screen.getByText("YAML")).toBeTruthy()
    expect(screen.getByText("History")).toBeTruthy()
    expect(screen.getByText("kind")).toBeTruthy()
  })

  it("highlights keys in blue", () => {
    const { container } = render(
      <ResourceDetail resource={mockResource} yaml={sampleYaml} />,
    )
    // key spans are rendered as <span style="color: #2563eb">key</span>
    // jsdom normalizes hex to rgb
    const keySpan = Array.from(container.querySelectorAll("span")).find(
      (el) => el.textContent === "kind" && el.children.length === 0,
    )
    expect(keySpan).toBeTruthy()
    expect(keySpan!.style.color).toBeTruthy()
  })

  it("highlights string values in green", () => {
    const { container } = render(
      <ResourceDetail resource={mockResource} yaml={sampleYaml} />,
    )
    const strSpan = Array.from(container.querySelectorAll("span")).find(
      (el) => el.textContent === '"my-pod"' && el.children.length === 0,
    )
    expect(strSpan).toBeTruthy()
    // color should be the green used for strings
    expect(strSpan!.style.color).not.toBe("")
  })

  it("highlights numbers in orange", () => {
    const { container } = render(
      <ResourceDetail resource={mockResource} yaml={sampleYaml} />,
    )
    const numSpan = Array.from(container.querySelectorAll("span")).find(
      (el) => el.textContent === "3" && el.children.length === 0,
    )
    expect(numSpan).toBeTruthy()
    expect(numSpan!.style.color).not.toBe("")
  })

  it("highlights bool/null in purple", () => {
    const { container } = render(
      <ResourceDetail resource={mockResource} yaml={sampleYaml} />,
    )
    const boolSpan = Array.from(container.querySelectorAll("span")).find(
      (el) => el.textContent === "true" && el.children.length === 0,
    )
    expect(boolSpan).toBeTruthy()
    expect(boolSpan!.style.color).not.toBe("")
  })

  it("highlights comments in gray", () => {
    const { container } = render(
      <ResourceDetail resource={mockResource} yaml={sampleYaml} />,
    )
    const commentSpan = Array.from(container.querySelectorAll("span")).find(
      (el) => el.textContent === "# a comment" && el.children.length === 0,
    )
    expect(commentSpan).toBeTruthy()
    expect(commentSpan!.style.color).not.toBe("")
  })

  it("switches to history tab and fetches", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
        json: async () => [],
        text: async () => "",
      }))

    render(<ResourceDetail resource={mockResource} yaml={sampleYaml} />)
    fireEvent.click(screen.getByText("History"))

    await waitFor(() => {
      expect(vi.mocked(fetch as typeof globalThis.fetch)).toHaveBeenCalledWith(
        "/api/resources/test-cluster/Pod/default/my-pod/history",
      )
    })
  })

  it("renders history entries", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
        json: async () => historyEntries,
        text: async () => "",
      }))

    render(<ResourceDetail resource={mockResource} yaml={sampleYaml} />)
    fireEvent.click(screen.getByText("History"))

    await waitFor(() => {
      expect(screen.getByText("abc12345")).toBeTruthy() // sha.slice(0, 8)
      expect(screen.getByText("update pod")).toBeTruthy()
    })
  })

  it("loads diff on history entry click", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce({
        json: async () => historyEntries,
        text: async () => "",
      })
      .mockResolvedValueOnce({
        json: async () => ({
          before: "kind: Pod\n",
          after: "kind: Pod\nspec:\n",
        }),
        text: async () => "",
      })
    vi.stubGlobal("fetch", fetchMock)

    render(<ResourceDetail resource={mockResource} yaml={sampleYaml} />)
    fireEvent.click(screen.getByText("History"))
    await waitFor(() => expect(screen.getByText("abc12345")).toBeTruthy())

    fireEvent.click(screen.getByText("update pod"))

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/resources/test-cluster/Pod/default/my-pod/diff?from=def67890efgh&to=abc12345abcd",
      )
    })
  })

  it("renders diff panes", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce({
        json: async () => historyEntries,
        text: async () => "",
      })
      .mockResolvedValueOnce({
        json: async () => ({
          before: "before: yaml\n",
          after: "after: yaml\n",
        }),
        text: async () => "",
      })
    vi.stubGlobal("fetch", fetchMock)

    render(<ResourceDetail resource={mockResource} yaml={sampleYaml} />)
    fireEvent.click(screen.getByText("History"))
    await waitFor(() => expect(screen.getByText("abc12345")).toBeTruthy())
    fireEvent.click(screen.getByText("update pod"))

    await waitFor(() => {
      expect(screen.queryByText("click a commit to see diff")).toBeNull()
    })
  })

  it("resets to yaml tab when resource changes", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
        json: async () => [],
        text: async () => "",
      }))

    const { rerender } = render(
      <ResourceDetail resource={mockResource} yaml={sampleYaml} />,
    )

    fireEvent.click(screen.getByText("History"))
    await waitFor(() => expect(screen.getByText("no history")).toBeTruthy())

    const otherResource: ResourceMeta = { ...mockResource, name: "other-pod" }
    rerender(<ResourceDetail resource={otherResource} yaml="kind: Pod\n" />)

    await waitFor(() => {
      expect(screen.queryByText("no history")).toBeNull()
    })
  })
})
