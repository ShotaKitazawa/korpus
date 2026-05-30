import { useEffect, useState } from "react"
import type { ResourceMeta } from "../App.tsx"

interface HistoryEntry {
  sha: string
  timestamp: string
  message: string
}

interface DiffResult {
  before: string
  after: string
}

interface Props {
  resource: ResourceMeta | null
  yaml: string
}

export default function ResourceDetail({ resource, yaml }: Props) {
  const [tab, setTab] = useState<"yaml" | "history">("yaml")
  const [history, setHistory] = useState<HistoryEntry[]>([])
  const [diff, setDiff] = useState<DiffResult | null>(null)
  const [selectedSHA, setSelectedSHA] = useState("")

  useEffect(() => {
    setTab("yaml")
    setHistory([])
    setDiff(null)
    setSelectedSHA("")
  }, [resource])

  useEffect(() => {
    if (tab !== "history" || !resource) return
    const { cluster, kind, namespace, name } = resource
    fetch(`/api/resources/${cluster}/${kind}/${namespace}/${name}/history`)
      .then((r) => r.json())
      .then((data) => setHistory(data ?? []))
      .catch(console.error)
  }, [tab, resource])

  const loadDiff = (sha: string, idx: number) => {
    if (!resource || idx + 1 >= history.length) return
    const prevSHA = history[idx + 1].sha
    const { cluster, kind, namespace, name } = resource
    setSelectedSHA(sha)
    fetch(
      `/api/resources/${cluster}/${kind}/${namespace}/${name}/diff?from=${prevSHA}&to=${sha}`,
    )
      .then((r) => r.json())
      .then(setDiff)
      .catch(console.error)
  }

  if (!resource) {
    return <div style={{ color: "#888" }}>select a resource</div>
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div
        style={{
          display: "flex",
          gap: 8,
          padding: "4px 0",
          borderBottom: "1px solid #ccc",
        }}
      >
        <button
          onClick={() => setTab("yaml")}
          style={{
            background: "none",
            border: "none",
            cursor: "pointer",
            fontFamily: "monospace",
            fontWeight: tab === "yaml" ? "bold" : undefined,
            borderBottom:
              tab === "yaml" ? "2px solid #333" : "2px solid transparent",
            padding: "2px 8px",
          }}
        >
          YAML
        </button>
        <button
          onClick={() => setTab("history")}
          style={{
            background: "none",
            border: "none",
            cursor: "pointer",
            fontFamily: "monospace",
            fontWeight: tab === "history" ? "bold" : undefined,
            borderBottom:
              tab === "history" ? "2px solid #333" : "2px solid transparent",
            padding: "2px 8px",
          }}
        >
          History
        </button>
      </div>

      {tab === "yaml" && (
        <pre
          style={{
            margin: 0,
            fontSize: 12,
            whiteSpace: "pre-wrap",
            wordBreak: "break-all",
            overflowY: "auto",
            flex: 1,
          }}
        >
          {yaml}
        </pre>
      )}

      {tab === "history" && (
        <div style={{ display: "flex", flex: 1, overflow: "hidden" }}>
          <div
            style={{
              width: 260,
              borderRight: "1px solid #ccc",
              overflowY: "auto",
              fontSize: 11,
            }}
          >
            {history.length === 0 && (
              <div style={{ padding: 8, color: "#888" }}>no history</div>
            )}
            {history.map((entry, idx) => (
              <div
                key={entry.sha}
                onClick={() => loadDiff(entry.sha, idx)}
                style={{
                  padding: "6px 8px",
                  borderBottom: "1px solid #eee",
                  cursor: idx + 1 < history.length ? "pointer" : "default",
                  background: selectedSHA === entry.sha ? "#e0e0ff" : undefined,
                }}
              >
                <div style={{ color: "#666", fontFamily: "monospace" }}>
                  {entry.sha.slice(0, 8)}
                </div>
                <div
                  style={{
                    color: "#333",
                    whiteSpace: "pre-wrap",
                    wordBreak: "break-all",
                  }}
                >
                  {entry.message.trim()}
                </div>
                <div style={{ color: "#999" }}>
                  {new Date(entry.timestamp).toLocaleString()}
                </div>
              </div>
            ))}
          </div>
          <div style={{ flex: 1, overflow: "hidden", display: "flex" }}>
            {diff ? (
              <>
                <pre
                  style={{
                    flex: 1,
                    margin: 0,
                    padding: 8,
                    fontSize: 11,
                    overflowY: "auto",
                    background: "#fff8f8",
                    borderRight: "1px solid #ccc",
                    whiteSpace: "pre-wrap",
                    wordBreak: "break-all",
                  }}
                >
                  {diff.before}
                </pre>
                <pre
                  style={{
                    flex: 1,
                    margin: 0,
                    padding: 8,
                    fontSize: 11,
                    overflowY: "auto",
                    background: "#f8fff8",
                    whiteSpace: "pre-wrap",
                    wordBreak: "break-all",
                  }}
                >
                  {diff.after}
                </pre>
              </>
            ) : (
              <div style={{ padding: 8, color: "#888", fontSize: 11 }}>
                click a commit to see diff
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
