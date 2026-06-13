import { useEffect, useState } from "react";
import type { GVKInfo } from "../api.ts";

interface Props {
  gvks: GVKInfo[];
  value: string;
  onChange: (info: GVKInfo | null) => void;
}

export default function KindSelect({ gvks, value, onChange }: Props) {
  const [inputValue, setInputValue] = useState(value);
  const [isFocused, setIsFocused] = useState(false);

  useEffect(() => {
    if (!isFocused) setInputValue(value);
  }, [value, isFocused]);

  const handleFocus = () => {
    setIsFocused(true);
    setInputValue("");
  };

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const v = e.target.value;
    setInputValue(v);
    const match = gvks.find((g) => `${g.group}/${g.version}/${g.kind}` === v);
    if (match) onChange(match);
  };

  const handleBlur = () => {
    setIsFocused(false);
    const match = gvks.find((g) => `${g.group}/${g.version}/${g.kind}` === inputValue);
    if (!match) setInputValue(value);
  };

  const handleClear = () => {
    setInputValue("");
    onChange(null);
  };

  return (
    <div style={{ display: "inline-flex", alignItems: "center", gap: 2 }}>
      <input
        list="gvks-list"
        placeholder="group/version/kind"
        value={isFocused ? inputValue : value}
        onFocus={handleFocus}
        onChange={handleChange}
        onBlur={handleBlur}
        style={{ width: 200 }}
      />
      {value && (
        <button
          onMouseDown={(e) => e.preventDefault()}
          onClick={handleClear}
          style={{
            fontFamily: "monospace",
            fontSize: 12,
            cursor: "pointer",
            padding: "1px 5px",
            border: "1px solid #ccc",
            borderRadius: 2,
            background: "none",
            lineHeight: 1,
          }}
        >
          ×
        </button>
      )}
      <datalist id="gvks-list">
        {gvks.map((g) => (
          <option
            key={`${g.group}/${g.version}/${g.kind}`}
            value={`${g.group}/${g.version}/${g.kind}`}
          />
        ))}
      </datalist>
    </div>
  );
}
