import type { KindInfo } from "../api.ts"

interface Props {
  kinds: KindInfo[]
  value: string
  onChange: (info: KindInfo | null) => void
}

export default function KindSelect({ kinds, value, onChange }: Props) {
  return (
    <>
      <input
        list="kinds-list"
        placeholder="group/kind"
        value={value}
        onChange={(e) => {
          const v = e.target.value
          const match = kinds.find((k) => `${k.group}/${k.kind}` === v)
          onChange(match ?? null)
        }}
        style={{ width: 160 }}
      />
      <datalist id="kinds-list">
        {kinds.map((k) => (
          <option key={`${k.group}/${k.kind}`} value={`${k.group}/${k.kind}`} />
        ))}
      </datalist>
    </>
  )
}
