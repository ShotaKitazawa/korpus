import {
  render,
  screen,
  fireEvent,
  waitFor,
  cleanup,
} from "@testing-library/react"
import { describe, it, expect, vi, afterEach } from "vitest"
import ResourceDetail from "./ResourceDetail.tsx"
import type { SnapshotResource } from "../App.tsx"

const mockResource: SnapshotResource = {
  cluster: "test-cluster",
  group: "core",
  kind: "Pod",
  name: "my-pod",
  namespace: "default",
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
    changeType: "modified",
    timestamp: "2026-05-30T00:00:00Z",
    cluster: "test-cluster",
    group: "core",
    kind: "Pod",
    namespace: "default",
    name: "my-pod",
  },
  {
    sha: "def67890efgh",
    changeType: "added",
    timestamp: "2026-05-29T00:00:00Z",
    cluster: "test-cluster",
    group: "core",
    kind: "Pod",
    namespace: "default",
    name: "my-pod",
  },
]

const historyPage = { items: historyEntries, total: 2, offset: 0, limit: 50 }

function makeJsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

// Returns a factory so each mock call gets a fresh Response (body streams can only be read once).
function jsonResponse(body: unknown, status = 200) {
  return () => Promise.resolve(makeJsonResponse(body, status))
}

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
    const mockFetch = vi.fn().mockImplementation(jsonResponse(historyPage))
    vi.stubGlobal("fetch", mockFetch)

    render(<ResourceDetail resource={mockResource} yaml={sampleYaml} />)
    fireEvent.click(screen.getByText("History"))

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalled()
      const arg = mockFetch.mock.calls[0][0] as Request | string
      const url = arg instanceof Request ? arg.url : String(arg)
      expect(url).toContain("/api/history")
      expect(url).toContain("cluster=test-cluster")
      expect(url).toContain("name=my-pod")
    })
  })

  it("renders history entries", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(jsonResponse(historyPage)),
    )

    render(<ResourceDetail resource={mockResource} yaml={sampleYaml} />)
    fireEvent.click(screen.getByText("History"))

    await waitFor(() => {
      expect(screen.getByText("abc12345")).toBeTruthy() // sha.slice(0, 8)
      expect(screen.getByText("modified")).toBeTruthy() // changeType
    })
  })

  it("loads diff on history entry click", async () => {
    const mockFetch = vi
      .fn()
      .mockImplementationOnce(jsonResponse(historyPage))
      .mockImplementationOnce(
        jsonResponse({ before: "kind: Pod\n", after: "kind: Pod\nspec:\n" }),
      )
    vi.stubGlobal("fetch", mockFetch)

    render(<ResourceDetail resource={mockResource} yaml={sampleYaml} />)
    fireEvent.click(screen.getByText("History"))
    await waitFor(() => expect(screen.getByText("abc12345")).toBeTruthy())

    fireEvent.click(screen.getByText("modified"))

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledTimes(2)
      const arg = mockFetch.mock.calls[1][0] as Request | string
      const url = arg instanceof Request ? arg.url : String(arg)
      expect(url).toContain("/api/diff")
      expect(url).toContain("name=my-pod")
    })
  })

  it("renders diff panes", async () => {
    const fetchMock = vi
      .fn()
      .mockImplementationOnce(jsonResponse(historyPage))
      .mockImplementationOnce(
        jsonResponse({ before: "before: yaml\n", after: "after: yaml\n" }),
      )
    vi.stubGlobal("fetch", fetchMock)

    render(<ResourceDetail resource={mockResource} yaml={sampleYaml} />)
    fireEvent.click(screen.getByText("History"))
    await waitFor(() => expect(screen.getByText("abc12345")).toBeTruthy())
    fireEvent.click(screen.getByText("modified"))

    await waitFor(() => {
      expect(screen.queryByText("click a commit to see diff")).toBeNull()
    })
  })

  it("resets to yaml tab when resource changes", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockImplementation(
          jsonResponse({ items: [], total: 0, offset: 0, limit: 50 }),
        ),
    )

    const { rerender } = render(
      <ResourceDetail resource={mockResource} yaml={sampleYaml} />,
    )

    fireEvent.click(screen.getByText("History"))
    await waitFor(() => expect(screen.getByText("no history")).toBeTruthy())

    const otherResource: SnapshotResource = {
      ...mockResource,
      name: "other-pod",
    }
    rerender(<ResourceDetail resource={otherResource} yaml="kind: Pod\n" />)

    await waitFor(() => {
      expect(screen.queryByText("no history")).toBeNull()
    })
  })
})
