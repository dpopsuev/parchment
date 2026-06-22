//go:build !cgo

package parchment

import "errors"

const driverLibSQL driverKind = 2

// LibSQLConfig holds tunable parameters for the libSQL store.
// When built without CGO, the libSQL driver is unavailable.
type LibSQLConfig struct {
	Path           string `json:"path,omitempty" yaml:"path,omitempty"`
	BusyTimeoutMs  int    `json:"busy_timeout_ms,omitempty" yaml:"busy_timeout_ms,omitempty"`
	ReaderPoolSize int    `json:"reader_pool_size,omitempty" yaml:"reader_pool_size,omitempty"`
}

// ErrLibSQLRequiresCGO is returned when trying to use libSQL without CGO.
var ErrLibSQLRequiresCGO = errors.New("libsql backend requires CGO_ENABLED=1")

// OpenLibSQLConfig is unavailable without CGO.
func OpenLibSQLConfig(_ LibSQLConfig) (*SQLiteStore, error) {
	return nil, ErrLibSQLRequiresCGO
}

// LibSQLBackend is unavailable without CGO.
type LibSQLBackend struct {
	store       *SQLiteStore
	snapshotter *Snapshotter
}

// NewLibSQLBackend is unavailable without CGO.
func NewLibSQLBackend(_ LibSQLConfig) (*LibSQLBackend, error) {
	return nil, ErrLibSQLRequiresCGO
}

func (b *LibSQLBackend) Store() Store              { return b.store }
func (b *LibSQLBackend) Snapshotter() *Snapshotter  { return b.snapshotter }
func (b *LibSQLBackend) Close() error               { return b.store.Close() }

var _ Backend = (*LibSQLBackend)(nil)
