import { useEffect, useState } from "react";
import { api, type VolatilityEntry, type FieldVolatilityEntry } from "../api.ts";

interface Props {
  onSelectResource: (entry: VolatilityEntry) => void;
  isMobile?: boolean;
}

export default function VolatilityView({ onSelectResource, isMobile }: Props) {
  const [entries, setEntries] = useState<VolatilityEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [commits, setCommits] = useState(50);
  const [threshold, setThreshold] = useState(0.5);
  const [selectedEntry, setSelectedEntry] = useState<VolatilityEntry | null>(null);
  const [fieldEntries, setFieldEntries] = useState<FieldVolatilityEntry[]>([]);
  const [fieldLoading, setFieldLoading] = useState(false);

  const load = () => {
    setLoading(true);
    api
      .GET("/api/volatility", {
        params: { query: { commits, threshold } },
      })
      .then(({ data }) => {
        setEntries(data?.items ?? []);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  };

  useEffect(() => {
    load();
  }, []);

  useEffect(() => {
    if (!selectedEntry) {
      setFieldEntries([]);
      return;
    }
    setFieldLoading(true);
    api
      .GET("/api/volatility/fields", {
        params: {
          query: {
            cluster: selectedEntry.cluster || undefined,
            group: selectedEntry.group,
            kind: selectedEntry.kind,
            namespace: selectedEntry.namespace || undefined,
            name: selectedEntry.name || undefined,
            commits,
          },
        },
      })
      .then(({ data }) => {
        setFieldEntries(data ?? []);
        setFieldLoading(false);
      })
      .catch(() => setFieldLoading(false));
  }, [selectedEntry, commits]);

  return (
    <div
      style={{
        display: "flex",
        flex: 1,
        overflow: isMobile ? "auto" : "hidden",
        flexDirection: isMobile ? "column" : "row",
        fontFamily: "monospace",
      }}
    >
      {/* left: volatility ranking */}
      <div
        style={{
          width: isMobile ? "100%" : "60%",
          borderRight: isMobile ? undefined : "1px solid #ccc",
          borderBottom: isMobile ? "1px solid #ccc" : undefined,
          overflowY: isMobile ? undefined : "auto",
          padding: 16,
        }}
      >
        <div
          style={{
            display: "flex",
            gap: 12,
            alignItems: "center",
            marginBottom: 12,
            flexWrap: "wrap",
          }}
        >
          <label style={{ fontSize: 12, color: "#666" }}>
            lookback commits:
            <input
              type="number"
              value={commits}
              min={1}
              max={500}
              onChange={(e) => setCommits(Number(e.target.value))}
              style={{
                marginLeft: 4,
                width: 60,
                fontFamily: "monospace",
                fontSize: 12,
              }}
            />
          </label>
          <label style={{ fontSize: 12, color: "#666" }}>
            threshold:
            <input
              type="number"
              value={threshold}
              min={0}
              max={1}
              step={0.1}
              onChange={(e) => setThreshold(Number(e.target.value))}
              style={{
                marginLeft: 4,
                width: 50,
                fontFamily: "monospace",
                fontSize: 12,
              }}
            />
          </label>
          <button
            onClick={load}
            style={{
              fontFamily: "monospace",
              fontSize: 12,
              cursor: "pointer",
              padding: "2px 10px",
            }}
          >
            Refresh
          </button>
          {loading && <span style={{ fontSize: 12, color: "#888" }}>loading…</span>}
        </div>

        {!loading && entries.length === 0 && (
          <div style={{ color: "#888", fontSize: 13 }}>
            no high-volatility resources found (threshold: {(threshold * 100).toFixed(0)}%)
          </div>
        )}

        {entries.length > 0 && (
          <table style={{ borderCollapse: "collapse", fontSize: 12, width: "100%" }}>
            <thead>
              <tr style={{ borderBottom: "1px solid #ccc", textAlign: "left" }}>
                <th style={{ padding: "4px 12px 4px 0" }}>resource</th>
                <th style={{ padding: "4px 12px 4px 0" }}>cluster</th>
                <th style={{ padding: "4px 12px 4px 0", textAlign: "right" }}>changed</th>
                <th style={{ padding: "4px 8px 4px 0", textAlign: "right" }}>ratio</th>
                <th style={{ padding: "4px 0" }} />
              </tr>
            </thead>
            <tbody>
              {entries.map((e, i) => {
                const pct = (e.ratio * 100).toFixed(0);
                const heat =
                  e.ratio >= 0.9
                    ? "#c00"
                    : e.ratio >= 0.7
                      ? "#d66"
                      : e.ratio >= 0.5
                        ? "#b84"
                        : "#666";
                const isSelected =
                  selectedEntry?.cluster === e.cluster &&
                  selectedEntry?.group === e.group &&
                  selectedEntry?.kind === e.kind &&
                  selectedEntry?.namespace === e.namespace &&
                  selectedEntry?.name === e.name;
                return (
                  <tr
                    key={i}
                    style={{
                      borderBottom: "1px solid #eee",
                      cursor: "pointer",
                      background: isSelected ? "#e0e0ff" : undefined,
                    }}
                    onClick={() => setSelectedEntry(e)}
                  >
                    <td style={{ padding: "5px 12px 5px 0", color: "#333" }}>
                      {e.group}/{e.kind}/{e.namespace ? e.namespace + "/" : ""}
                      {e.name}
                    </td>
                    <td style={{ padding: "5px 12px 5px 0", color: "#666" }}>{e.cluster}</td>
                    <td
                      style={{
                        padding: "5px 12px 5px 0",
                        textAlign: "right",
                        color: "#555",
                      }}
                    >
                      {e.count}/{e.total}
                    </td>
                    <td
                      style={{
                        padding: "5px 8px 5px 0",
                        textAlign: "right",
                        fontWeight: "bold",
                        color: heat,
                      }}
                    >
                      {pct}%
                    </td>
                    <td style={{ padding: "5px 0" }}>
                      <button
                        onClick={(ev) => {
                          ev.stopPropagation();
                          onSelectResource(e);
                        }}
                        title={`view resources of kind ${e.kind}`}
                        style={{
                          fontFamily: "monospace",
                          fontSize: 11,
                          cursor: "pointer",
                          background: "none",
                          border: "1px solid #ccc",
                          borderRadius: 2,
                          padding: "1px 4px",
                          color: "#555",
                        }}
                      >
                        →
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      {/* right: field volatility */}
      <div style={{ flex: 1, overflowY: "auto", padding: 16 }}>
        {!selectedEntry ? (
          <div style={{ color: "#888", fontSize: 13 }}>
            select a resource to see field volatility
          </div>
        ) : (
          <>
            <div style={{ fontSize: 12, color: "#555", marginBottom: 8 }}>
              {selectedEntry.group}/{selectedEntry.kind}/
              {selectedEntry.namespace ? selectedEntry.namespace + "/" : ""}
              {selectedEntry.name}
            </div>
            {fieldLoading && <div style={{ fontSize: 12, color: "#888" }}>loading…</div>}
            {!fieldLoading && fieldEntries.length === 0 && (
              <div style={{ fontSize: 12, color: "#888" }}>no field data</div>
            )}
            {!fieldLoading && fieldEntries.length > 0 && (
              <table
                style={{
                  borderCollapse: "collapse",
                  fontSize: 12,
                  width: "100%",
                }}
              >
                <thead>
                  <tr
                    style={{
                      borderBottom: "1px solid #ccc",
                      textAlign: "left",
                    }}
                  >
                    <th style={{ padding: "4px 12px 4px 0" }}>field</th>
                    <th style={{ padding: "4px 12px 4px 0", textAlign: "right" }}>changed</th>
                    <th style={{ padding: "4px 8px 4px 0", textAlign: "right" }}>ratio</th>
                  </tr>
                </thead>
                <tbody>
                  {fieldEntries.map((f, i) => {
                    const pct = (f.ratio * 100).toFixed(0);
                    const heat =
                      f.ratio >= 0.9
                        ? "#c00"
                        : f.ratio >= 0.7
                          ? "#d66"
                          : f.ratio >= 0.5
                            ? "#b84"
                            : "#666";
                    return (
                      <tr key={i} style={{ borderBottom: "1px solid #eee" }}>
                        <td
                          style={{
                            padding: "4px 12px 4px 0",
                            color: "#333",
                            wordBreak: "break-all",
                          }}
                        >
                          {f.field}
                        </td>
                        <td
                          style={{
                            padding: "4px 12px 4px 0",
                            textAlign: "right",
                            color: "#555",
                          }}
                        >
                          {f.count}/{f.total}
                        </td>
                        <td
                          style={{
                            padding: "4px 8px 4px 0",
                            textAlign: "right",
                            fontWeight: "bold",
                            color: heat,
                          }}
                        >
                          {pct}%
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
          </>
        )}
      </div>
    </div>
  );
}
