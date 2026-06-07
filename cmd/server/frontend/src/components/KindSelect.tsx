import type { GVKInfo } from "../api.ts"

interface Props {
  gvks: GVKInfo[]
  value: string
  onChange: (info: GVKInfo | null) => void
}

export default function KindSelect({ gvks, value, onChange }: Props) {
  return (
    <>
      <input
        list="gvks-list"
        placeholder="group/version/kind"
        value={value}
        onChange={(e) => {
          const v = e.target.value
          const match = gvks.find(
            (g) => `${g.group}/${g.version}/${g.kind}` === v,
          )
          onChange(match ?? null)
        }}
        style={{ width: 200 }}
      />
      <datalist id="gvks-list">
        {gvks.map((g) => (
          <option
            key={`${g.group}/${g.version}/${g.kind}`}
            value={`${g.group}/${g.version}/${g.kind}`}
          />
        ))}
      </datalist>
    </>
  )
}
