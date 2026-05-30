interface Props {
  value: string
  onChange: (v: string) => void
}

export default function LabelFilter({ value, onChange }: Props) {
  return (
    <input
      type="text"
      placeholder="labels (app=nginx)"
      value={value}
      onChange={(e) => onChange(e.target.value)}
      style={{ width: 160 }}
    />
  )
}
