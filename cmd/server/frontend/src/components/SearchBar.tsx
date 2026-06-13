interface Props {
  query: string;
  onChange: (q: string) => void;
}

export default function SearchBar({ query, onChange }: Props) {
  return (
    <input
      type="search"
      placeholder="CEL expression…"
      value={query}
      onChange={(e) => onChange(e.target.value)}
      style={{ flex: 1 }}
    />
  );
}
