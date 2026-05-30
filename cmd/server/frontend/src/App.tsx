import { useEffect, useState } from "react"
import ClusterList from "./components/ClusterList.tsx"
import KindSelect from "./components/KindSelect.tsx"
import NamespaceList from "./components/NamespaceList.tsx"
import ResourceDetail from "./components/ResourceDetail.tsx"
import ResourceList from "./components/ResourceList.tsx"
import SearchBar from "./components/SearchBar.tsx"

export interface ResourceMeta {
  cluster: string
  kind: string
  name: string
  namespace: string
  labels: Record<string, string> | null
}

export default function App() {
  const [clusters, setClusters] = useState<string[]>([])
  const [selectedCluster, setSelectedCluster] = useState("")
  const [namespaces, setNamespaces] = useState<string[]>([])
  const [selectedNamespace, setSelectedNamespace] = useState("")
  const [kinds, setKinds] = useState<string[]>([])
  const [selectedKind, setSelectedKind] = useState("")
  const [resources, setResources] = useState<ResourceMeta[]>([])
  const [selected, setSelected] = useState<ResourceMeta | null>(null)
  const [detail, setDetail] = useState("")
  const [searchQuery, setSearchQuery] = useState("")

  useEffect(() => {
    fetch("/api/clusters")
      .then((r) => r.json())
      .then(setClusters)
      .catch(console.error)
  }, [])

  useEffect(() => {
    const params = new URLSearchParams()
    if (selectedCluster) params.set("cluster", selectedCluster)
    fetch(`/api/namespaces?${params}`)
      .then((r) => r.json())
      .then(setNamespaces)
      .catch(console.error)
  }, [selectedCluster])

  useEffect(() => {
    const params = new URLSearchParams()
    if (selectedCluster) params.set("cluster", selectedCluster)
    if (selectedNamespace) params.set("namespace", selectedNamespace)
    fetch(`/api/kinds?${params}`)
      .then((r) => r.json())
      .then(setKinds)
      .catch(console.error)
  }, [selectedCluster, selectedNamespace])

  useEffect(() => {
    if (selectedKind && searchQuery) {
      const params = new URLSearchParams({ kind: selectedKind })
      if (selectedCluster) params.set("cluster", selectedCluster)
      if (selectedNamespace) params.set("namespace", selectedNamespace)
      params.set("q", searchQuery)
      fetch(`/api/query?${params}`)
        .then((r) => r.json())
        .then((data) => setResources(data ?? []))
        .catch(console.error)
      return
    }
    const params = new URLSearchParams()
    if (selectedCluster) params.set("cluster", selectedCluster)
    if (selectedKind) params.set("kind", selectedKind)
    if (selectedNamespace) params.set("namespace", selectedNamespace)
    fetch(`/api/resources?${params}`)
      .then((r) => r.json())
      .then((data) => setResources(data ?? []))
      .catch(console.error)
  }, [selectedCluster, selectedNamespace, selectedKind, searchQuery])

  useEffect(() => {
    if (!selected) {
      setDetail("")
      return
    }
    fetch(
      `/api/resources/${selected.cluster}/${selected.kind}/${selected.namespace}/${selected.name}`,
    )
      .then((r) => r.text())
      .then(setDetail)
      .catch(console.error)
  }, [selected])

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
