import type { ResourceMeta } from "../App.tsx"

interface Props {
  resources: ResourceMeta[]
  total: number
  offset: number
  limit: number
  selected: ResourceMeta | null
  onSelect: (r: ResourceMeta) => void
  onOffsetChange: (offset: number) => void
}

function formatAge(ts: string): string {
  const diffMs = Date.now() - new Date(ts).getTime()
  const days = Math.floor(diffMs / 86400000)
  if (days >= 1) return `${days}d`
  const hours = Math.floor(diffMs / 3600000)
  if (hours >= 1) return `${hours}h`
  const minutes = Math.floor(diffMs / 60000)
  return `${minutes}m`
}

export default function ResourceList({
  resources,
  total,
  offset,
  limit,
  selected,
  onSelect,
  onOffsetChange,
}: Props) {
  const hasPrev = offset > 0
  const hasNext = offset + limit < total

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div style={{ flex: 1, overflowY: "auto" }}>
        {resources.length === 0 && (
          <div style={{ padding: 8, color: "#888" }}>no resources</div>
        )}
        {resources.map((r) => {
          const key = `${r.cluster}/${r.kind}/${r.namespace}/${r.name}`
          const isSelected =
            selected?.cluster === r.cluster &&
            selected?.kind === r.kind &&
            selected?.name === r.name &&
            selected?.namespace === r.namespace
          return (
            <div
              key={key}
              style={{
                padding: "4px 8px",
                cursor: "pointer",
                background: isSelected ? "#e0e0ff" : undefined,
                borderBottom: "1px solid #eee",
              }}
              onClick={() => onSelect(r)}
            >
              <div
                style={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "baseline",
                }}
              >
                <span style={{ fontSize: 11, color: "#666" }}>{r.kind}</span>
                {r.creationTimestamp && (
                  <span style={{ fontSize: 10, color: "#aaa" }}>
                    {formatAge(r.creationTimestamp)}
                  </span>
                )}
              </div>
              <div>{r.name}</div>
              {r.namespace && (
                <div style={{ fontSize: 11, color: "#888" }}>{r.namespace}</div>
              )}
            </div>
          )
        })}
      </div>

      {total > 0 && (
        <div
          style={{
            borderTop: "1px solid #ccc",
            padding: "4px 8px",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            fontSize: 11,
            color: "#666",
            flexShrink: 0,
          }}
        >
          <button
            onClick={() => onOffsetChange(Math.max(0, offset - limit))}
            disabled={!hasPrev}
            style={{
              fontFamily: "monospace",
              fontSize: 11,
              cursor: hasPrev ? "pointer" : "default",
              background: "none",
              border: "none",
              color: hasPrev ? "#333" : "#ccc",
              padding: "0 4px",
            }}
          >
            ← Prev
          </button>
          <span>
            {offset + 1}–{Math.min(offset + limit, total)} / {total}
          </span>
          <button
            onClick={() => onOffsetChange(offset + limit)}
            disabled={!hasNext}
            style={{
              fontFamily: "monospace",
              fontSize: 11,
              cursor: hasNext ? "pointer" : "default",
              background: "none",
              border: "none",
              color: hasNext ? "#333" : "#ccc",
              padding: "0 4px",
            }}
          >
            Next →
          </button>
        </div>
      )}
    </div>
  )
}
