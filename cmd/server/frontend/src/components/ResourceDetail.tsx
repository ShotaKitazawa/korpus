import { useEffect, useState } from "react";
import { api, type DiffResult, type ChangeEvent } from "../api.ts";
import type { SnapshotResource } from "../App.tsx";

const colors = {
  key: "#2563eb",
  string: "#16a34a",
  number: "#d97706",
  bool: "#7c3aed",
  comment: "#9ca3af",
  separator: "#6b7280",
};

function YamlLineContent({ line }: { line: string }) {
  if (/^---\s*$/.test(line)) {
    return <span style={{ color: colors.separator }}>{line}</span>;
  }
  const commentMatch = line.match(/^(\s*)(#.*)$/);
  if (commentMatch) {
    return (
      <>
        {commentMatch[1]}
        <span style={{ color: colors.comment }}>{commentMatch[2]}</span>
      </>
    );
  }
  const kvMatch = line.match(/^(\s*-?\s*)([^:]+)(:)(\s+)(.*)?$/);
  if (kvMatch) {
    const [, indent, key, colon, space, val = ""] = kvMatch;
    return (
      <>
        {indent}
        <span style={{ color: colors.key }}>{key}</span>
        {colon}
        {space}
        <YamlValue value={val} />
      </>
    );
  }
  const listMatch = line.match(/^(\s*-\s+)(.*)$/);
  if (listMatch) {
    return (
      <>
        <span style={{ color: colors.separator }}>{listMatch[1]}</span>
        <YamlValue value={listMatch[2]} />
      </>
    );
  }
  return <>{line}</>;
}

function YamlLine({ line }: { line: string }) {
  return (
    <span>
      <YamlLineContent line={line} />
      {"\n"}
    </span>
  );
}

function YamlValue({ value }: { value: string }) {
  if (value === "") return null;
  if (value === "true" || value === "false" || value === "null" || value === "~") {
    return <span style={{ color: colors.bool }}>{value}</span>;
  }
  if (/^-?\d+(\.\d+)?([eE][+-]?\d+)?$/.test(value)) {
    return <span style={{ color: colors.number }}>{value}</span>;
  }
  if (
    value.startsWith('"') ||
    value.startsWith("'") ||
    value.startsWith("|") ||
    value.startsWith(">")
  ) {
    return <span style={{ color: colors.string }}>{value}</span>;
  }
  return <span>{value}</span>;
}

function YamlHighlight({ text }: { text: string }) {
  const lines = text.split("\n");
  return (
    <pre
      style={{
        margin: 0,
        fontSize: 12,
        whiteSpace: "pre-wrap",
        wordBreak: "break-all",
        overflowY: "auto",
        flex: 1,
      }}
    >
      {lines.map((line, i) => (
        <YamlLine key={i} line={line} />
      ))}
    </pre>
  );
}

type DiffLine =
  | {
      type: "equal" | "remove" | "add";
      line: string;
    }
  | { type: "empty" };

function computeLineDiff(
  before: string,
  after: string,
): {
  left: DiffLine[];
  right: DiffLine[];
} {
  const a = before.split("\n");
  const b = after.split("\n");
  const m = a.length;
  const n = b.length;

  const dp: number[][] = Array.from({ length: m + 1 }, () =>
    Array.from<number>({ length: n + 1 }).fill(0),
  );
  for (let i = 1; i <= m; i++) {
    for (let j = 1; j <= n; j++) {
      dp[i][j] =
        a[i - 1] === b[j - 1] ? dp[i - 1][j - 1] + 1 : Math.max(dp[i - 1][j], dp[i][j - 1]);
    }
  }

  const left: DiffLine[] = [];
  const right: DiffLine[] = [];
  let i = m;
  let j = n;
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && a[i - 1] === b[j - 1]) {
      left.unshift({ type: "equal", line: a[i - 1] });
      right.unshift({ type: "equal", line: b[j - 1] });
      i--;
      j--;
    } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
      left.unshift({ type: "empty" });
      right.unshift({ type: "add", line: b[j - 1] });
      j--;
    } else {
      left.unshift({ type: "remove", line: a[i - 1] });
      right.unshift({ type: "empty" });
      i--;
    }
  }

  return { left, right };
}

function DiffLineView({ line }: { line: DiffLine }) {
  const bg =
    line.type === "remove"
      ? "#ffecec"
      : line.type === "add"
        ? "#eaffea"
        : line.type === "empty"
          ? "#f5f5f5"
          : undefined;
  return (
    <div
      style={{
        background: bg,
        minHeight: "1.5em",
        lineHeight: "1.5em",
        paddingLeft: 4,
        whiteSpace: "pre-wrap",
        wordBreak: "break-all",
        fontFamily: "monospace",
        fontSize: 11,
      }}
    >
      {line.type !== "empty" && <YamlLineContent line={line.line} />}
    </div>
  );
}

function SideBySideDiff({ diff }: { diff: DiffResult }) {
  const { left, right } = computeLineDiff(diff.before, diff.after);
  return (
    <>
      <div style={{ flex: 1, overflow: "auto", borderRight: "1px solid #ccc" }}>
        {left.map((line, i) => (
          <DiffLineView key={i} line={line} />
        ))}
      </div>
      <div style={{ flex: 1, overflow: "auto" }}>
        {right.map((line, i) => (
          <DiffLineView key={i} line={line} />
        ))}
      </div>
    </>
  );
}

function getFromTo(
  shas: string[],
  history: ChangeEvent[],
): {
  from: string;
  to: string;
} | null {
  if (shas.length !== 2) return null;
  const idx0 = history.findIndex((e) => e.sha === shas[0]);
  const idx1 = history.findIndex((e) => e.sha === shas[1]);
  if (idx0 === -1 || idx1 === -1) return null;
  // history is newest-first, so higher idx = older commit = "from"
  return idx0 > idx1 ? { from: shas[0], to: shas[1] } : { from: shas[1], to: shas[0] };
}

interface Props {
  resource: SnapshotResource | null;
  yaml: string;
}

export default function ResourceDetail({ resource, yaml }: Props) {
  const [tab, setTab] = useState<"yaml" | "history">("yaml");
  const [history, setHistory] = useState<ChangeEvent[]>([]);
  const [selectedSHAs, setSelectedSHAs] = useState<string[]>([]);
  const [diff, setDiff] = useState<DiffResult | null>(null);

  useEffect(() => {
    setTab("yaml");
    setHistory([]);
    setDiff(null);
    setSelectedSHAs([]);
  }, [resource]);

  useEffect(() => {
    if (tab !== "history" || !resource) return;
    const { cluster, group, kind, namespace, name } = resource;
    api
      .GET("/api/history", {
        params: {
          query: {
            cluster,
            group,
            kind,
            namespace: namespace || undefined,
            name,
          },
        },
      })
      .then(({ data }) => setHistory(data?.items ?? []));
  }, [tab, resource]);

  useEffect(() => {
    const fromTo = getFromTo(selectedSHAs, history);
    if (!fromTo || !resource) {
      setDiff(null);
      return;
    }
    const { from, to } = fromTo;
    const { cluster, group, kind, namespace, name } = resource;
    api
      .GET("/api/diff", {
        params: {
          query: {
            cluster,
            group,
            kind,
            namespace: namespace || undefined,
            name,
            from,
            to,
          },
        },
      })
      .then(({ data }) => {
        if (data) setDiff(data);
      });
  }, [selectedSHAs, resource, history]);

  const handleCommitClick = (sha: string) => {
    setSelectedSHAs((prev) => {
      if (prev.includes(sha)) return prev.filter((s) => s !== sha);
      if (prev.length < 2) return [...prev, sha];
      return [prev[1], sha];
    });
  };

  if (!resource) {
    return <div style={{ color: "#888" }}>select a resource</div>;
  }

  const fromTo = getFromTo(selectedSHAs, history);

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div
        style={{
          display: "flex",
          gap: 8,
          padding: "4px 0",
          borderBottom: "1px solid #ccc",
        }}
      >
        <button
          onClick={() => setTab("yaml")}
          style={{
            background: "none",
            border: "none",
            cursor: "pointer",
            fontFamily: "monospace",
            fontWeight: tab === "yaml" ? "bold" : undefined,
            borderBottom: tab === "yaml" ? "2px solid #333" : "2px solid transparent",
            padding: "2px 8px",
          }}
        >
          YAML
        </button>
        <button
          onClick={() => setTab("history")}
          style={{
            background: "none",
            border: "none",
            cursor: "pointer",
            fontFamily: "monospace",
            fontWeight: tab === "history" ? "bold" : undefined,
            borderBottom: tab === "history" ? "2px solid #333" : "2px solid transparent",
            padding: "2px 8px",
          }}
        >
          History
        </button>
      </div>

      {tab === "yaml" && <YamlHighlight text={yaml} />}

      {tab === "history" && (
        <div style={{ display: "flex", flex: 1, overflow: "hidden" }}>
          <div
            style={{
              width: 260,
              borderRight: "1px solid #ccc",
              overflowY: "auto",
              fontSize: 11,
            }}
          >
            {history.length === 0 && <div style={{ padding: 8, color: "#888" }}>no history</div>}
            {history.map((entry) => {
              const isFrom = fromTo?.from === entry.sha;
              const isTo = fromTo?.to === entry.sha;
              const isSelected = selectedSHAs.includes(entry.sha);
              const bg = isFrom ? "#fff3cd" : isTo ? "#d4edda" : isSelected ? "#e8e8ff" : undefined;
              return (
                <div
                  key={entry.sha}
                  onClick={() => handleCommitClick(entry.sha)}
                  style={{
                    padding: "6px 8px",
                    borderBottom: "1px solid #eee",
                    cursor: "pointer",
                    background: bg,
                  }}
                >
                  <div
                    style={{
                      display: "flex",
                      justifyContent: "space-between",
                      alignItems: "center",
                    }}
                  >
                    <span style={{ color: "#666", fontFamily: "monospace" }}>
                      {entry.sha.slice(0, 8)}
                    </span>
                    {isFrom && (
                      <span
                        style={{
                          fontSize: 9,
                          background: "#ffc107",
                          color: "#333",
                          padding: "0 4px",
                          borderRadius: 2,
                          fontFamily: "monospace",
                        }}
                      >
                        FROM
                      </span>
                    )}
                    {isTo && (
                      <span
                        style={{
                          fontSize: 9,
                          background: "#28a745",
                          color: "#fff",
                          padding: "0 4px",
                          borderRadius: 2,
                          fontFamily: "monospace",
                        }}
                      >
                        TO
                      </span>
                    )}
                  </div>
                  <div
                    style={{
                      color: "#333",
                      whiteSpace: "pre-wrap",
                      wordBreak: "break-all",
                    }}
                  >
                    {entry.changeType}
                  </div>
                  <div style={{ color: "#999" }}>{new Date(entry.timestamp).toLocaleString()}</div>
                </div>
              );
            })}
          </div>

          <div
            style={{
              flex: 1,
              overflow: "hidden",
              display: "flex",
              flexDirection: "column",
            }}
          >
            {selectedSHAs.length < 2 ? (
              <div style={{ padding: 8, color: "#888", fontSize: 11 }}>
                {selectedSHAs.length === 0
                  ? "select two commits to compare"
                  : "select one more commit to compare"}
              </div>
            ) : diff && fromTo ? (
              <>
                <div
                  style={{
                    display: "flex",
                    borderBottom: "1px solid #ccc",
                    fontFamily: "monospace",
                    fontSize: 10,
                    background: "#fafafa",
                    flexShrink: 0,
                  }}
                >
                  <div
                    style={{
                      flex: 1,
                      padding: "2px 8px",
                      color: "#666",
                      borderRight: "1px solid #ccc",
                    }}
                  >
                    {fromTo.from.slice(0, 8)}
                  </div>
                  <div style={{ flex: 1, padding: "2px 8px", color: "#666" }}>
                    {fromTo.to.slice(0, 8)}
                  </div>
                </div>
                <div style={{ display: "flex", flex: 1, overflow: "hidden" }}>
                  <SideBySideDiff diff={diff} />
                </div>
              </>
            ) : (
              <div style={{ padding: 8, color: "#888", fontSize: 11 }}>loading...</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
