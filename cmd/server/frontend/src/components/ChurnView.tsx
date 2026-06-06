import { useEffect, useState } from "react"
import { api, type VolatilityEntry } from "../api.ts"

interface Props {
  onSelectKind: (kind: string) => void
}

export default function ChurnView({ onSelectKind }: Props) {
  const [entries, setEntries] = useState<VolatilityEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [commits, setCommits] = useState(50)
  const [threshold, setThreshold] = useState(0.5)

  const load = () => {
    setLoading(true)
    api
      .GET("/api/volatility", {
        params: { query: { commits, threshold } },
      })
      .then(({ data }) => {
        setEntries(data?.items ?? [])
        setLoading(false)
      })
      .catch(() => setLoading(false))
  }

  useEffect(() => {
    load()
  }, [])

  return (
    <div
      style={{
        padding: 16,
        fontFamily: "monospace",
        overflowY: "auto",
        height: "100%",
      }}
    >
      <div
        style={{
          display: "flex",
          gap: 12,
          alignItems: "center",
          marginBottom: 12,
        }}
      >
        <label style={{ fontSize: 12, color: "#666" }}>
          lookback commits:
          <input
            type="number"
            value={commits}
            min={1}
            max={500}
            onChange={(e) => setCommits(Number(e.target.value))}
            style={{
              marginLeft: 4,
              width: 60,
              fontFamily: "monospace",
              fontSize: 12,
            }}
          />
        </label>
        <label style={{ fontSize: 12, color: "#666" }}>
          threshold:
          <input
            type="number"
            value={threshold}
            min={0}
            max={1}
            step={0.1}
            onChange={(e) => setThreshold(Number(e.target.value))}
            style={{
              marginLeft: 4,
              width: 50,
              fontFamily: "monospace",
              fontSize: 12,
            }}
          />
        </label>
        <button
          onClick={load}
          style={{
            fontFamily: "monospace",
            fontSize: 12,
            cursor: "pointer",
            padding: "2px 10px",
          }}
        >
          Refresh
        </button>
        {loading && (
          <span style={{ fontSize: 12, color: "#888" }}>loading…</span>
        )}
      </div>

      {!loading && entries.length === 0 && (
        <div style={{ color: "#888", fontSize: 13 }}>
          no high-volatility resources found (threshold:{" "}
          {(threshold * 100).toFixed(0)}%)
        </div>
      )}

      {entries.length > 0 && (
        <table
          style={{ borderCollapse: "collapse", fontSize: 12, width: "100%" }}
        >
          <thead>
            <tr style={{ borderBottom: "1px solid #ccc", textAlign: "left" }}>
              <th style={{ padding: "4px 12px 4px 0" }}>resource</th>
              <th style={{ padding: "4px 12px 4px 0" }}>cluster</th>
              <th style={{ padding: "4px 12px 4px 0", textAlign: "right" }}>
                changed
              </th>
              <th style={{ padding: "4px 8px 4px 0", textAlign: "right" }}>
                ratio
              </th>
            </tr>
          </thead>
          <tbody>
            {entries.map((e, i) => {
              const pct = (e.ratio * 100).toFixed(0)
              const heat =
                e.ratio >= 0.9
                  ? "#c00"
                  : e.ratio >= 0.7
                    ? "#d66"
                    : e.ratio >= 0.5
                      ? "#b84"
                      : "#666"
              return (
                <tr
                  key={i}
                  style={{
                    borderBottom: "1px solid #eee",
                    cursor: "pointer",
                  }}
                  onClick={() => onSelectKind(e.kind)}
                  title={`filter by ${e.kind}`}
                >
                  <td style={{ padding: "5px 12px 5px 0", color: "#333" }}>
                    {e.group}/{e.kind}/{e.namespace ? e.namespace + "/" : ""}
                    {e.name}
                  </td>
                  <td style={{ padding: "5px 12px 5px 0", color: "#666" }}>
                    {e.cluster}
                  </td>
                  <td
                    style={{
                      padding: "5px 12px 5px 0",
                      textAlign: "right",
                      color: "#555",
                    }}
                  >
                    {e.count}/{e.total}
                  </td>
                  <td
                    style={{
                      padding: "5px 8px 5px 0",
                      textAlign: "right",
                      fontWeight: "bold",
                      color: heat,
                    }}
                  >
                    {pct}%
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
    </div>
  )
}
