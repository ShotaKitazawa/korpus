import type { ResourceMeta } from "../App.tsx"

interface Props {
  resources: ResourceMeta[]
  selected: ResourceMeta | null
  onSelect: (r: ResourceMeta) => void
}

export default function ResourceList({ resources, selected, onSelect }: Props) {
  if (resources.length === 0) {
    return <div style={{ padding: 8, color: "#888" }}>no resources</div>
  }
  return (
    <div>
      {resources.map((r) => {
        const key = `${r.kind}/${r.namespace}/${r.name}`
        const isSelected =
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
            <div style={{ fontSize: 11, color: "#666" }}>{r.kind}</div>
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
