interface Props {
  groups: string[]
  selected: string
  onSelect: (group: string) => void
}

export default function GroupList({ groups, selected, onSelect }: Props) {
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
      {groups.sort().map((g) => (
        <div
          key={g}
          style={{
            cursor: "pointer",
            fontWeight: selected === g ? "bold" : undefined,
            padding: "2px 4px",
          }}
          onClick={() => onSelect(g)}
        >
          {g}
        </div>
      ))}
    </div>
  )
}
