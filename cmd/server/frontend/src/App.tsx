import { useEffect, useRef, useState } from "react"
import { api, type ResourceMeta } from "./api.ts"
import ClusterList from "./components/ClusterList.tsx"
import KindSelect from "./components/KindSelect.tsx"
import LabelFilter from "./components/LabelFilter.tsx"
import NamespaceList from "./components/NamespaceList.tsx"
import ResourceDetail from "./components/ResourceDetail.tsx"
import ResourceList from "./components/ResourceList.tsx"
import SearchBar from "./components/SearchBar.tsx"

export type { ResourceMeta }

function readParam(key: string): string {
  return new URLSearchParams(window.location.search).get(key) ?? ""
}

function syncUrl(state: {
  cluster: string
  namespace: string
  kind: string
  labels: string
  q: string
  selected: ResourceMeta | null
}) {
  const params = new URLSearchParams()
  if (state.cluster) params.set("cluster", state.cluster)
  if (state.namespace) params.set("namespace", state.namespace)
  if (state.kind) params.set("kind", state.kind)
  if (state.labels) params.set("labels", state.labels)
  if (state.q) params.set("q", state.q)
  if (state.selected) {
    params.set("selCluster", state.selected.cluster)
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
  const [namespaces, setNamespaces] = useState<string[]>([])
  const [selectedNamespace, setSelectedNamespace] = useState(() =>
    readParam("namespace"),
  )
  const [kinds, setKinds] = useState<string[]>([])
  const [selectedKind, setSelectedKind] = useState(() => readParam("kind"))
  const [labelFilter, setLabelFilter] = useState(() => readParam("labels"))
  const [resources, setResources] = useState<ResourceMeta[]>([])
  const [selected, setSelected] = useState<ResourceMeta | null>(null)
  const [detail, setDetail] = useState("")
  const [searchQuery, setSearchQuery] = useState(() => readParam("q"))

  const pendingSelect = useRef({
    cluster: readParam("selCluster"),
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
    let promise: Promise<ResourceMeta[]>
    if (selectedKind && searchQuery) {
      promise = api
        .GET("/api/query", {
          params: {
            query: {
              kind: selectedKind,
              cluster: selectedCluster || undefined,
              namespace: selectedNamespace || undefined,
              labels: labelFilter || undefined,
              q: searchQuery,
            },
          },
        })
        .then(({ data }) => data ?? [])
    } else {
      promise = api
        .GET("/api/resources", {
          params: {
            query: {
              cluster: selectedCluster || undefined,
              kind: selectedKind || undefined,
              namespace: selectedNamespace || undefined,
              labels: labelFilter || undefined,
            },
          },
        })
        .then(({ data }) => data ?? [])
    }
    promise.then((list) => {
      setResources(list)
      const p = pendingSelect.current
      if (p.name) {
        const match = list.find(
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
            kind: "",
            namespace: "",
            name: "",
          }
        }
      }
    })
  }, [
    selectedCluster,
    selectedNamespace,
    selectedKind,
    searchQuery,
    labelFilter,
  ])

  useEffect(() => {
    if (!selected) {
      setDetail("")
      return
    }
    api
      .GET("/api/resources/{cluster}/{kind}/{namespace}/{name}", {
        params: {
          path: {
            cluster: selected.cluster,
            kind: selected.kind,
            namespace: selected.namespace,
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
      namespace: selectedNamespace,
      kind: selectedKind,
      labels: labelFilter,
      q: searchQuery,
      selected,
    })
  }, [
    selectedCluster,
    selectedNamespace,
    selectedKind,
    labelFilter,
    searchQuery,
    selected,
  ])

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
            setSelectedNamespace("")
            setSearchQuery("")
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
          }}
        >
          <KindSelect
            kinds={kinds}
            value={selectedKind}
            onChange={(k) => {
              setSelectedKind(k)
              setSelected(null)
            }}
          />
          <LabelFilter
            value={labelFilter}
            onChange={(v) => {
              setLabelFilter(v)
              setSelected(null)
            }}
          />
          <SearchBar
            query={searchQuery}
            onChange={(q) => {
              setSearchQuery(q)
              setSelected(null)
            }}
          />
        </div>
        <div style={{ display: "flex", flex: 1, overflow: "hidden" }}>
          <div
            style={{
              width: 300,
              borderRight: "1px solid #ccc",
              overflowY: "auto",
            }}
          >
            <ResourceList
              resources={resources}
              onSelect={setSelected}
              selected={selected}
            />
          </div>
          <div style={{ flex: 1, overflow: "hidden", padding: 8 }}>
            <ResourceDetail resource={selected} yaml={detail} />
          </div>
        </div>
      </div>
    </div>
  )
}
