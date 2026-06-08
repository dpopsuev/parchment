package parchment

// Backend bundles a Store with its satellite concerns (snapshots) as a coherent unit.
// Callers construct a Backend via a technology-specific factory (SQLiteBackend, future KuzuBackend)
// and pass it to service.Open — the construction path never needs to know which DB is in use.
type Backend interface {
	Store() Store
	Snapshotter() *Snapshotter
	Close() error
}

// SQLiteBackend is the production Backend backed by SQLite with WAL and local snapshots.
type SQLiteBackend struct {
	store       *SQLiteStore
	snapshotter *Snapshotter
}

// NewSQLiteBackend opens a SQLiteStore and wires it to a LocalSnapshotBackend.
func NewSQLiteBackend(cfg SQLiteConfig) (*SQLiteBackend, error) {
	s, err := OpenSQLiteConfig(cfg)
	if err != nil {
		return nil, err
	}
	var snapshotter *Snapshotter
	if cfg.Path != "" && cfg.Path != ":memory:" {
		backend := NewLocalSnapshotBackend(cfg.Path, s.Writer())
		snapshotter = NewSnapshotter(backend, s)
	}
	return &SQLiteBackend{store: s, snapshotter: snapshotter}, nil
}

func (b *SQLiteBackend) Store() Store            { return b.store }
func (b *SQLiteBackend) Snapshotter() *Snapshotter { return b.snapshotter }
func (b *SQLiteBackend) Close() error              { return b.store.Close() }
