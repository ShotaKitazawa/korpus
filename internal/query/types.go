package query

import (
	"time"

	"github.com/ShotaKitazawa/korpus/internal/index"
)

type DiffResult struct {
	Before string
	After  string
}

// SnapshotResult is returned by both GetCurrentSnapshot and GetHistoricalSnapshot.
// CommitSHA and CommitTime are empty/zero for current-state queries.
type SnapshotResult struct {
	Items      []index.ResourceMeta
	Total      int
	CommitSHA  string
	CommitTime time.Time
}

type VolatilityResult struct {
	Cluster   string  `json:"cluster"`
	Group     string  `json:"group"`
	Kind      string  `json:"kind"`
	Namespace string  `json:"namespace"`
	Name      string  `json:"name"`
	Count     int     `json:"count"`
	Total     int     `json:"total"`
	Ratio     float64 `json:"ratio"`
}

type FieldVolatilityResult struct {
	Field string  `json:"field"`
	Count int     `json:"count"`
	Total int     `json:"total"`
	Ratio float64 `json:"ratio"`
}
