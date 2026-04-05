# Parchment — Artifact Graph Engine

Reusable domain library for structured work graph artifacts with DAG support, lifecycle management, and full-text search. Part of the Aeon ecosystem.

## What Parchment Provides

- **Artifact types**: Artifact, Section, Edge, Filter, Schema, KindDef, ComponentMap, Annotation
- **Protocol**: 73+ business logic methods (CRUD, graph ops, lifecycle, validation, search)
- **Two Stores**: SQLiteStore (WAL, FTS5, snapshots) and MemoryStore (atomic JSON)
- **Rendering**: Markdown, Table, GroupedTable, JSON output
- **Keygen**: Deterministic scope key derivation with collision avoidance
- **Graph**: Topological sort via dominikbraun/graph, cycle detection, cascade

## Zero Transport Dependencies

Parchment has no MCP, HTTP, CLI, or framework imports. Only:
- `modernc.org/sqlite` — pure Go SQLite (no CGO)
- `github.com/dominikbraun/graph` — DAG operations

## Working with Parchment

```bash
# Build
go build ./...

# Test
go test ./... -count=1

# Test with race detector
go test -race ./... -count=1

# Benchmarks
go test -bench=. -benchmem -count=1 ./...

# Lint
golangci-lint run ./...

# Preflight (all checks)
make preflight
```

## Architecture

Single flat package (`parchment`), file-per-concern:

| File | Content |
|---|---|
| `artifact.go` | Artifact, Section, Edge, Filter, ComponentMap, Annotation, IDConfig, IDTemplate |
| `constants.go` | Status/Kind/Field/Relation constants, ErrArtifactNotFound |
| `schema.go` | Schema, KindDef, Guards, validation (60+ methods), DefaultSchema |
| `store.go` | ISP interfaces: ArtifactStore, GraphStore, SequenceStore, ScopeStore, Store |
| `sqlite.go` | SQLiteStore (WAL, FTS5, snapshots, migrations) |
| `memstore.go` | MemoryStore (in-memory + atomic JSON Save/Load) |
| `protocol.go` | Protocol (73+ methods), ProtocolConfig, CreateInput, ListInput |
| `stash.go` | StashStore for failed creation recovery |
| `seed.go` | Template/config seeding from directory |
| `capsule.go` | Portable export/import (tar.gz) |
| `keygen.go` | DeriveKey, ExtractConsonantSkeleton |
| `render.go` | RenderMarkdown, RenderTable, RenderJSON |
| `snapshot.go` | Snapshotter with pluggable backends |
| `snapshot_local.go` | LocalSnapshotBackend (file copy) |

## Testing

- **Property tests** (pgregory.net/rapid): keygen, schema, filter, IDs, transitions
- **Contract tests**: Store interface suite runs against SQLiteStore and MemoryStore
- **Benchmarks**: Create, List, TopoSort, Search, NextScopedID, Walk
- **Migration compat**: verifies old schema → new schema round-trip
- **Race clean**: `go test -race` passes

## Consumers

- **Scribe** — MCP server wrapping Parchment for AI agent work tracking
- **Djinn** — AI agent harness (future — will import Parchment for artifact management)
