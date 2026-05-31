import type { ResourceMeta } from "../App.tsx"

interface Props {
  resources: ResourceMeta[]
  selected: ResourceMeta | null
  onSelect: (r: ResourceMeta) => void
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

export default function ResourceList({ resources, selected, onSelect }: Props) {
  if (resources.length === 0) {
    return <div style={{ padding: 8, color: "#888" }}>no resources</div>
  }
  return (
    <div>
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
  )
}
