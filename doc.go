// Package parchment provides the domain model, business protocol, and
// reference storage implementation for structured work graph artifacts.
//
// Parchment is the reusable artifact graph engine for the Aeon ecosystem.
// It contains all domain logic with zero dependency on transport packages
// (MCP SDK, cobra, net/http). Any consumer can import parchment to create,
// query, and manage artifacts with DAG support, lifecycle management, and
// full-text search.
//
// Two Store implementations are provided:
//   - SQLiteStore: production-grade with WAL, FTS5, snapshots
//   - MemoryStore: lightweight with atomic JSON persistence
package parchment
