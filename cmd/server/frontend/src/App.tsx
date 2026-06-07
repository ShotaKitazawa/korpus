import { useEffect, useRef, useState } from "react"
import { api, type SnapshotResource, type KindInfo } from "./api.ts"
import VolatilityView from "./components/VolatilityView.tsx"
import ClusterList from "./components/ClusterList.tsx"
import KindSelect from "./components/KindSelect.tsx"
import NamespaceList from "./components/NamespaceList.tsx"
import ResourceDetail from "./components/ResourceDetail.tsx"
import ResourceList from "./components/ResourceList.tsx"
import SearchBar from "./components/SearchBar.tsx"

export type { SnapshotResource }

const DEFAULT_LIMIT = 50

function readParam(key: string): string {
  return new URLSearchParams(window.location.search).get(key) ?? ""
}

function readIntParam(key: string, fallback: number): number {
  const v = new URLSearchParams(window.location.search).get(key)
  const n = v ? parseInt(v, 10) : NaN
  return isNaN(n) ? fallback : n
}

function syncUrl(state: {
  cluster: string
  group: string
  namespace: string
  kind: string
  q: string
  offset: number
  selected: SnapshotResource | null
  view: string
}) {
  const params = new URLSearchParams()
  if (state.cluster) params.set("cluster", state.cluster)
  if (state.group) params.set("group", state.group)
  if (state.namespace) params.set("namespace", state.namespace)
  if (state.kind) params.set("kind", state.kind)
  if (state.q) params.set("q", state.q)
  if (state.offset > 0) params.set("offset", String(state.offset))
  if (state.view && state.view !== "resources") params.set("view", state.view)
  if (state.selected) {
    params.set("selCluster", state.selected.cluster)
    params.set("selGroup", state.selected.group)
    params.set("selKind", state.selected.kind)
    params.set("selNamespace", state.selected.namespace)
    params.set("selName", state.selected.name)
  }
  const search = params.toString()
    ? "?" + params.toString()
    : window.location.pathname
  history.replaceState(null, "", search)
}

export default function App() {
  const [clusters, setClusters] = useState<string[]>([])
  const [selectedCluster, setSelectedCluster] = useState(() =>
    readParam("cluster"),
  )
  const [selectedGroup, setSelectedGroup] = useState(() => readParam("group"))
  const [namespaces, setNamespaces] = useState<string[]>([])
  const [selectedNamespace, setSelectedNamespace] = useState(() =>
    readParam("namespace"),
  )
  const [kinds, setKinds] = useState<KindInfo[]>([])
  const [selectedKind, setSelectedKind] = useState(() => readParam("kind"))
  const [resources, setResources] = useState<SnapshotResource[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(() => readIntParam("offset", 0))
  const [selected, setSelected] = useState<SnapshotResource | null>(null)
  const [detail, setDetail] = useState("")
  const [searchQuery, setSearchQuery] = useState(() => readParam("q"))
  const [view, setView] = useState<"resources" | "volatility">(() =>
    readParam("view") === "volatility" ? "volatility" : "resources",
  )

  const pendingSelect = useRef({
    cluster: readParam("selCluster"),
    group: readParam("selGroup"),
    kind: readParam("selKind"),
    namespace: readParam("selNamespace"),
    name: readParam("selName"),
  })

  useEffect(() => {
    api.GET("/api/clusters").then(({ data }) => {
      if (data) setClusters(data)
    })
  }, [])

  useEffect(() => {
    api
      .GET("/api/namespaces", {
        params: { query: { cluster: selectedCluster || undefined } },
      })
      .then(({ data }) => {
        if (data) setNamespaces(data)
      })
  }, [selectedCluster])

  useEffect(() => {
    api
      .GET("/api/kinds", {
        params: {
          query: {
            cluster: selectedCluster || undefined,
            namespace: selectedNamespace || undefined,
          },
        },
      })
      .then(({ data }) => {
        if (data) setKinds(data)
      })
  }, [selectedCluster, selectedNamespace])

  useEffect(() => {
    let cancelled = false

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
          }))
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
        }))
    }

    fetchPage().then(({ items, total }) => {
      if (cancelled) return
      setResources(items)
      setTotal(total)
      const p = pendingSelect.current
      if (p.name) {
        const match = items.find(
          (r) =>
            r.name === p.name &&
            r.kind === p.kind &&
            r.namespace === p.namespace &&
            r.cluster === p.cluster,
        )
        if (match) {
          setSelected(match)
          pendingSelect.current = {
            cluster: "",
            group: "",
            kind: "",
            namespace: "",
            name: "",
          }
        }
      }
    })

    return () => {
      cancelled = true
    }
  }, [
    selectedCluster,
    selectedGroup,
    selectedNamespace,
    selectedKind,
    searchQuery,
    offset,
  ])

  useEffect(() => {
    if (!selected) {
      setDetail("")
      return
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
      .then(({ data }) => setDetail(data ?? ""))
  }, [selected])

  useEffect(() => {
    syncUrl({
      cluster: selectedCluster,
      group: selectedGroup,
      namespace: selectedNamespace,
      kind: selectedKind,
      q: searchQuery,
      offset,
      selected,
      view,
    })
  }, [
    selectedCluster,
    selectedGroup,
    selectedNamespace,
    selectedKind,
    searchQuery,
    offset,
    selected,
    view,
  ])

  const resetFilters = () => {
    setSelected(null)
    setOffset(0)
  }

  return (
    <div style={{ display: "flex", height: "100vh", fontFamily: "monospace" }}>
      <div
        style={{
          width: 200,
          borderRight: "1px solid #ccc",
          overflowY: "auto",
          padding: 8,
        }}
      >
        <ClusterList
          clusters={clusters}
          selected={selectedCluster}
          onSelect={(c) => {
            setSelectedCluster(c)
            setSelectedGroup("")
            setSelectedNamespace("")
            setSelectedKind("")
            setSearchQuery("")
            setOffset(0)
            setSelected(null)
          }}
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
          onSelect={(ns) => {
            setSelectedNamespace(ns)
            setSearchQuery("")
            setOffset(0)
            setSelected(null)
          }}
        />
      </div>
      <div style={{ flex: 1, display: "flex", flexDirection: "column" }}>
        <div
          style={{
            padding: 8,
            borderBottom: "1px solid #ccc",
            display: "flex",
            gap: 8,
            alignItems: "center",
          }}
        >
          <KindSelect
            kinds={kinds}
            value={selectedKind ? `${selectedGroup}/${selectedKind}` : ""}
            onChange={(info) => {
              setSelectedGroup(info?.group ?? "")
              setSelectedKind(info?.kind ?? "")
              setOffset(0)
              resetFilters()
            }}
          />
          <SearchBar
            query={searchQuery}
            onChange={(q) => {
              setSearchQuery(q)
              setOffset(0)
              resetFilters()
            }}
          />
          <button
            onClick={() =>
              setView(view === "volatility" ? "resources" : "volatility")
            }
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
            onSelectResource={(group, kind) => {
              setSelectedGroup(group)
              setSelectedKind(kind)
              setOffset(0)
              setSelected(null)
              setView("resources")
            }}
          />
        ) : (
          <div style={{ display: "flex", flex: 1, overflow: "hidden" }}>
            <div
              style={{
                width: 300,
                borderRight: "1px solid #ccc",
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
                  setOffset(o)
                  setSelected(null)
                }}
              />
            </div>
            <div style={{ flex: 1, overflow: "hidden", padding: 8 }}>
              <ResourceDetail resource={selected} yaml={detail} />
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
