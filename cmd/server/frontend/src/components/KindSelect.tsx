interface Props {
  kinds: string[]
  value: string
  onChange: (kind: string) => void
}

export default function KindSelect({ kinds, value, onChange }: Props) {
  return (
    <>
      <input
        list="kinds-list"
        placeholder="kind"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        style={{ width: 120 }}
      />
      <datalist id="kinds-list">
        {kinds.map((k) => (
          <option key={k} value={k} />
        ))}
      </datalist>
    </>
  )
}
