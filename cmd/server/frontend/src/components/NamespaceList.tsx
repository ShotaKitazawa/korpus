interface Props {
  namespaces: string[]
  selected: string
  onSelect: (ns: string) => void
}

export default function NamespaceList({
  namespaces,
  selected,
  onSelect,
}: Props) {
  return (
    <div>
      <div
        style={{
          cursor: "pointer",
          fontWeight: selected === "" ? "bold" : undefined,
          padding: "2px 4px",
        }}
        onClick={() => onSelect("")}
      >
        all
      </div>
      {namespaces.sort().map((ns) => (
        <div
          key={ns}
          style={{
            cursor: "pointer",
            fontWeight: selected === ns ? "bold" : undefined,
            padding: "2px 4px",
          }}
          onClick={() => onSelect(ns)}
        >
          {ns}
        </div>
      ))}
    </div>
  )
}
