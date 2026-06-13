interface Props {
  clusters: string[];
  selected: string;
  onSelect: (cluster: string) => void;
}

export default function ClusterList({ clusters, selected, onSelect }: Props) {
  return (
    <div>
      <div style={{ padding: "4px 4px 2px", fontSize: "0.75em", color: "#888" }}>cluster</div>
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
      {clusters.map((c) => (
        <div
          key={c}
          style={{
            cursor: "pointer",
            fontWeight: selected === c ? "bold" : undefined,
            padding: "2px 4px",
          }}
          onClick={() => onSelect(c)}
        >
          {c}
        </div>
      ))}
    </div>
  );
}
