import { useEffect, useRef, useState } from "react";
import { api, type SnapshotResource, type GVKInfo } from "./api.ts";
import VolatilityView from "./components/VolatilityView.tsx";
import ClusterList from "./components/ClusterList.tsx";
import KindSelect from "./components/KindSelect.tsx";
import NamespaceList from "./components/NamespaceList.tsx";
import ResourceDetail from "./components/ResourceDetail.tsx";
import ResourceList from "./components/ResourceList.tsx";
import SearchBar from "./components/SearchBar.tsx";

export type { SnapshotResource };

const DEFAULT_LIMIT = 50;
const MOBILE_BREAKPOINT = 768;

function useIsMobile(): boolean {
  const [isMobile, setIsMobile] = useState(() => window.innerWidth <= MOBILE_BREAKPOINT);
  useEffect(() => {
    const handler = () => setIsMobile(window.innerWidth <= MOBILE_BREAKPOINT);
    window.addEventListener("resize", handler);
    return () => window.removeEventListener("resize", handler);
  }, []);
  return isMobile;
}

function readParam(key: string): string {
  return new URLSearchParams(window.location.search).get(key) ?? "";
}

function readIntParam(key: string, fallback: number): number {
  const v = new URLSearchParams(window.location.search).get(key);
  const n = v ? parseInt(v, 10) : NaN;
  return isNaN(n) ? fallback : n;
}

function syncUrl(state: {
  cluster: string;
  group: string;
  version: string;
  namespace: string;
  kind: string;
  q: string;
  offset: number;
  selected: SnapshotResource | null;
  view: string;
}) {
  const params = new URLSearchParams();
  if (state.cluster) params.set("cluster", state.cluster);
  if (state.group) params.set("group", state.group);
  if (state.version) params.set("version", state.version);
  if (state.namespace) params.set("namespace", state.namespace);
  if (state.kind) params.set("kind", state.kind);
  if (state.q) params.set("q", state.q);
  if (state.offset > 0) params.set("offset", String(state.offset));
  if (state.view && state.view !== "resources") params.set("view", state.view);
  if (state.selected) {
    params.set("selCluster", state.selected.cluster);
    params.set("selGroup", state.selected.group);
    params.set("selKind", state.selected.kind);
    params.set("selNamespace", state.selected.namespace);
    params.set("selName", state.selected.name);
  }
  const search = params.toString() ? "?" + params.toString() : window.location.pathname;
  const cur = new URLSearchParams(window.location.search);
  const navChanged =
    (cur.get("cluster") ?? "") !== state.cluster ||
    (cur.get("group") ?? "") !== state.group ||
    (cur.get("namespace") ?? "") !== state.namespace ||
    (cur.get("kind") ?? "") !== state.kind ||
    (cur.get("view") ?? "") !== (state.view !== "resources" ? state.view : "");
  if (navChanged) {
    history.pushState(null, "", search);
  } else {
    history.replaceState(null, "", search);
  }
}

export default function App() {
  const [clusters, setClusters] = useState<string[]>([]);
  const [selectedCluster, setSelectedCluster] = useState(() => readParam("cluster"));
  const [selectedGroup, setSelectedGroup] = useState(() => readParam("group"));
  const [selectedVersion, setSelectedVersion] = useState(() => readParam("version"));
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [selectedNamespace, setSelectedNamespace] = useState(() => readParam("namespace"));
  const [gvks, setGVKs] = useState<GVKInfo[]>([]);
  const [selectedKind, setSelectedKind] = useState(() => readParam("kind"));
  const [resources, setResources] = useState<SnapshotResource[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(() => readIntParam("offset", 0));
  const [selected, setSelected] = useState<SnapshotResource | null>(null);
  const [detail, setDetail] = useState("");
  const [searchQuery, setSearchQuery] = useState(() => readParam("q"));
  const [view, setView] = useState<"resources" | "volatility">(() =>
    readParam("view") === "volatility" ? "volatility" : "resources",
  );
  const isMobile = useIsMobile();
  const [sidebarOpen, setSidebarOpen] = useState(false);

  const pendingSelect = useRef({
    cluster: readParam("selCluster"),
    group: readParam("selGroup"),
    kind: readParam("selKind"),
    namespace: readParam("selNamespace"),
    name: readParam("selName"),
  });

  useEffect(() => {
    const onPopState = () => {
      setSelectedCluster(readParam("cluster"));
      setSelectedGroup(readParam("group"));
      setSelectedVersion(readParam("version"));
      setSelectedNamespace(readParam("namespace"));
      setSelectedKind(readParam("kind"));
      setSearchQuery(readParam("q"));
      setOffset(readIntParam("offset", 0));
      setView(readParam("view") === "volatility" ? "volatility" : "resources");
      setSelected(null);
      const name = readParam("selName");
      if (name) {
        pendingSelect.current = {
          cluster: readParam("selCluster"),
          group: readParam("selGroup"),
          kind: readParam("selKind"),
          namespace: readParam("selNamespace"),
          name,
        };
      }
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  useEffect(() => {
    api.GET("/api/clusters").then(({ data }) => {
      if (data) setClusters(data);
    });
  }, []);

  useEffect(() => {
    api
      .GET("/api/namespaces", {
        params: { query: { cluster: selectedCluster || undefined } },
      })
      .then(({ data }) => {
        if (data) setNamespaces(data);
      });
  }, [selectedCluster]);

  useEffect(() => {
    api
      .GET("/api/gvks", {
        params: {
          query: {
            cluster: selectedCluster || undefined,
            namespace: selectedNamespace || undefined,
          },
        },
      })
      .then(({ data }) => {
        if (data) setGVKs(data);
      });
  }, [selectedCluster, selectedNamespace]);

  useEffect(() => {
    let cancelled = false;

    const fetchPage = () => {
      if (selectedKind && searchQuery) {
        return api
          .GET("/api/snapshot", {
            params: {
              query: {
                cluster: selectedCluster || undefined,
                group: selectedGroup || undefined,
                kind: selectedKind || undefined,
                namespace: selectedNamespace || undefined,
                cel: searchQuery || undefined,
                offset,
                limit: DEFAULT_LIMIT,
              },
            },
          })
          .then(({ data }) => ({
            items: (data?.items ?? []) as SnapshotResource[],
            total: data?.total ?? 0,
          }));
      }
      return api
        .GET("/api/snapshot", {
          params: {
            query: {
              cluster: selectedCluster || undefined,
              group: selectedGroup || undefined,
              kind: selectedKind || undefined,
              namespace: selectedNamespace || undefined,
              offset,
              limit: DEFAULT_LIMIT,
            },
          },
        })
        .then(({ data }) => ({
          items: (data?.items ?? []) as SnapshotResource[],
          total: data?.total ?? 0,
        }));
    };

    fetchPage().then(({ items, total }) => {
      if (cancelled) return;
      setResources(items);
      setTotal(total);
      const p = pendingSelect.current;
      if (p.name) {
        const match = items.find(
          (r) =>
            r.name === p.name &&
            r.kind === p.kind &&
            r.namespace === p.namespace &&
            r.cluster === p.cluster,
        );
        if (match) {
          setSelected(match);
          pendingSelect.current = {
            cluster: "",
            group: "",
            kind: "",
            namespace: "",
            name: "",
          };
        }
      }
    });

    return () => {
      cancelled = true;
    };
  }, [selectedCluster, selectedGroup, selectedNamespace, selectedKind, searchQuery, offset]);

  useEffect(() => {
    if (!selected) {
      setDetail("");
      return;
    }
    api
      .GET("/api/resource", {
        params: {
          query: {
            cluster: selected.cluster,
            group: selected.group,
            kind: selected.kind,
            namespace: selected.namespace || undefined,
            name: selected.name,
          },
        },
        parseAs: "text",
      })
      .then(({ data }) => setDetail(data ?? ""));
  }, [selected]);

  useEffect(() => {
    syncUrl({
      cluster: selectedCluster,
      group: selectedGroup,
      version: selectedVersion,
      namespace: selectedNamespace,
      kind: selectedKind,
      q: searchQuery,
      offset,
      selected,
      view,
    });
  }, [
    selectedCluster,
    selectedGroup,
    selectedVersion,
    selectedNamespace,
    selectedKind,
    searchQuery,
    offset,
    selected,
    view,
  ]);

  const resetFilters = () => {
    setSelected(null);
    setOffset(0);
  };

  const handleClusterSelect = (c: string) => {
    setSelectedCluster(c);
    setSelectedGroup("");
    setSelectedVersion("");
    setSelectedNamespace("");
    setSelectedKind("");
    setSearchQuery("");
    setOffset(0);
    setSelected(null);
    if (isMobile) setSidebarOpen(false);
  };

  const handleNamespaceSelect = (ns: string) => {
    setSelectedNamespace(ns);
    setSearchQuery("");
    setOffset(0);
    setSelected(null);
    if (isMobile) setSidebarOpen(false);
  };

  return (
    <div style={{ display: "flex", height: "100vh", fontFamily: "monospace" }}>
      {/* Mobile: backdrop to close sidebar */}
      {isMobile && sidebarOpen && (
        <div
          onClick={() => setSidebarOpen(false)}
          style={{
            position: "fixed",
            top: 0,
            left: 0,
            width: "100%",
            height: "100%",
            background: "rgba(0,0,0,0.3)",
            zIndex: 99,
          }}
        />
      )}

      {/* Left sidebar */}
      {(!isMobile || sidebarOpen) && (
        <div
          style={{
            ...(isMobile
              ? {
                  position: "fixed",
                  top: 0,
                  left: 0,
                  height: "100vh",
                  width: "80%",
                  maxWidth: 280,
                  zIndex: 100,
                  background: "#fff",
                }
              : { width: 200 }),
            borderRight: "1px solid #ccc",
            overflowY: "auto",
            padding: 8,
          }}
        >
          <ClusterList
            clusters={clusters}
            selected={selectedCluster}
            onSelect={handleClusterSelect}
          />
          <div
            style={{
              borderTop: "1px solid #eee",
              marginTop: 8,
              paddingTop: 4,
              fontSize: "0.75em",
              color: "#888",
            }}
          >
            namespace
          </div>
          <NamespaceList
            namespaces={namespaces}
            selected={selectedNamespace}
            onSelect={handleNamespaceSelect}
          />
        </div>
      )}

      <div style={{ flex: 1, display: "flex", flexDirection: "column", minWidth: 0 }}>
        <div
          style={{
            padding: 8,
            borderBottom: "1px solid #ccc",
            display: "flex",
            gap: 8,
            alignItems: "center",
            flexWrap: "wrap",
          }}
        >
          {isMobile && (
            <button
              onClick={() => setSidebarOpen(!sidebarOpen)}
              style={{
                fontFamily: "monospace",
                fontSize: 16,
                cursor: "pointer",
                padding: "2px 8px",
                background: "none",
                border: "1px solid #ccc",
                borderRadius: 2,
                lineHeight: 1.4,
              }}
            >
              ☰
            </button>
          )}
          <KindSelect
            gvks={gvks}
            value={selectedKind ? `${selectedGroup}/${selectedVersion}/${selectedKind}` : ""}
            onChange={(info) => {
              setSelectedGroup(info?.group ?? "");
              setSelectedVersion(info?.version ?? "");
              setSelectedKind(info?.kind ?? "");
              setOffset(0);
              resetFilters();
            }}
          />
          <SearchBar
            query={searchQuery}
            onChange={(q) => {
              setSearchQuery(q);
              setOffset(0);
              resetFilters();
            }}
          />
          <button
            onClick={() => setView(view === "volatility" ? "resources" : "volatility")}
            style={{
              fontFamily: "monospace",
              fontSize: 12,
              cursor: "pointer",
              padding: "2px 10px",
              background: view === "volatility" ? "#333" : undefined,
              color: view === "volatility" ? "#fff" : undefined,
              border: "1px solid #ccc",
              borderRadius: 2,
              marginLeft: "auto",
            }}
          >
            Volatility
          </button>
        </div>

        {view === "volatility" ? (
          <VolatilityView
            isMobile={isMobile}
            onSelectResource={(group, kind) => {
              setSelectedGroup(group);
              setSelectedVersion("");
              setSelectedKind(kind);
              setOffset(0);
              setSelected(null);
              setView("resources");
            }}
          />
        ) : (
          <div style={{ display: "flex", flex: 1, overflow: "hidden" }}>
            {/* On mobile: show list only when no resource is selected */}
            {(!isMobile || !selected) && (
              <div
                style={{
                  width: isMobile ? "100%" : 300,
                  borderRight: isMobile ? undefined : "1px solid #ccc",
                  overflow: "hidden",
                  display: "flex",
                  flexDirection: "column",
                }}
              >
                <ResourceList
                  resources={resources}
                  total={total}
                  offset={offset}
                  limit={DEFAULT_LIMIT}
                  onSelect={setSelected}
                  selected={selected}
                  onOffsetChange={(o) => {
                    setOffset(o);
                    setSelected(null);
                  }}
                />
              </div>
            )}
            {/* On mobile: show detail only when a resource is selected */}
            {(!isMobile || selected) && (
              <div style={{ flex: 1, overflow: "hidden", padding: 8 }}>
                <ResourceDetail
                  resource={selected}
                  yaml={detail}
                  isMobile={isMobile}
                  onBack={isMobile ? () => setSelected(null) : undefined}
                />
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
