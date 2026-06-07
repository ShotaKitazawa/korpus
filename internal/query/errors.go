package query

import "errors"

var (
	ErrClusterNotFound = errors.New("cluster not found")
	ErrNotFound        = errors.New("not found")
	ErrNotReady        = errors.New("cluster not ready")
)
