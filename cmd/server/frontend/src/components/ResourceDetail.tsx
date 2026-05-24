interface Props {
  yaml: string
}

export default function ResourceDetail({ yaml }: Props) {
  if (!yaml) {
    return <div style={{ color: "#888" }}>select a resource</div>
  }
  return (
    <pre
      style={{
        margin: 0,
        fontSize: 12,
        whiteSpace: "pre-wrap",
        wordBreak: "break-all",
      }}
    >
      {yaml}
    </pre>
  )
}
