import { useEffect, useState } from "react"
import { api, type DiffResult, type ChangeEvent } from "../api.ts"
import type { SnapshotResource } from "../App.tsx"

const colors = {
  key: "#2563eb",
  string: "#16a34a",
  number: "#d97706",
  bool: "#7c3aed",
  comment: "#9ca3af",
  separator: "#6b7280",
}

function YamlLine({ line }: { line: string }) {
  // separator
  if (/^---\s*$/.test(line)) {
    return (
      <span>
        <span style={{ color: colors.separator }}>{line}</span>
        {"\n"}
      </span>
    )
  }
  // comment
  const commentMatch = line.match(/^(\s*)(#.*)$/)
  if (commentMatch) {
    return (
      <span>
        {commentMatch[1]}
        <span style={{ color: colors.comment }}>{commentMatch[2]}</span>
        {"\n"}
      </span>
    )
  }
  // key: value
  const kvMatch = line.match(/^(\s*-?\s*)([^:]+)(:)(\s+)(.*)?$/)
  if (kvMatch) {
    const [, indent, key, colon, space, val = ""] = kvMatch
    return (
      <span>
        {indent}
        <span style={{ color: colors.key }}>{key}</span>
        {colon}
        {space}
        <YamlValue value={val} />
        {"\n"}
      </span>
    )
  }
  // list item or plain value
  const listMatch = line.match(/^(\s*-\s+)(.*)$/)
  if (listMatch) {
    return (
      <span>
        <span style={{ color: colors.separator }}>{listMatch[1]}</span>
        <YamlValue value={listMatch[2]} />
        {"\n"}
      </span>
    )
  }
  return (
    <span>
      {line}
      {"\n"}
    </span>
  )
}

function YamlValue({ value }: { value: string }) {
  if (value === "") return null
  if (
    value === "true" ||
    value === "false" ||
    value === "null" ||
    value === "~"
  ) {
    return <span style={{ color: colors.bool }}>{value}</span>
  }
  if (/^-?\d+(\.\d+)?([eE][+-]?\d+)?$/.test(value)) {
    return <span style={{ color: colors.number }}>{value}</span>
  }
  if (
    value.startsWith('"') ||
    value.startsWith("'") ||
    value.startsWith("|") ||
    value.startsWith(">")
  ) {
    return <span style={{ color: colors.string }}>{value}</span>
  }
  return <span>{value}</span>
}

function YamlHighlight({ text }: { text: string }) {
  const lines = text.split("\n")
  return (
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
      {lines.map((line, i) => (
        <YamlLine key={i} line={line} />
      ))}
    </pre>
  )
}

interface Props {
  resource: SnapshotResource | null
  yaml: string
}

export default function ResourceDetail({ resource, yaml }: Props) {
  const [tab, setTab] = useState<"yaml" | "history">("yaml")
  const [history, setHistory] = useState<ChangeEvent[]>([])
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
    const { cluster, group, kind, namespace, name } = resource
    api
      .GET("/api/history", {
        params: {
          query: {
            cluster,
            group,
            kind,
            namespace: namespace || undefined,
            name,
          },
        },
      })
      .then(({ data }) => setHistory(data?.items ?? []))
  }, [tab, resource])

  const loadDiff = (sha: string, idx: number) => {
    if (!resource || idx + 1 >= history.length) return
    const prevSHA = history[idx + 1].sha
    const { cluster, group, kind, namespace, name } = resource
    setSelectedSHA(sha)
    api
      .GET("/api/diff", {
        params: {
          query: {
            cluster,
            group,
            kind,
            namespace: namespace || undefined,
            name,
            from: prevSHA,
            to: sha,
          },
        },
      })
      .then(({ data }) => {
        if (data) setDiff(data)
      })
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

      {tab === "yaml" && <YamlHighlight text={yaml} />}

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
                  {entry.changeType}
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
                <div
                  style={{
                    flex: 1,
                    overflow: "auto",
                    background: "#fff8f8",
                    borderRight: "1px solid #ccc",
                    padding: 8,
                    fontSize: 11,
                  }}
                >
                  <YamlHighlight text={diff.before} />
                </div>
                <div
                  style={{
                    flex: 1,
                    overflow: "auto",
                    background: "#f8fff8",
                    padding: 8,
                    fontSize: 11,
                  }}
                >
                  <YamlHighlight text={diff.after} />
                </div>
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
