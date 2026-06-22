//go:build cgo

package parchment

import "testing"

// TestLibSQLStore_Contract runs the full Store contract against libSQL.
func TestLibSQLStore_Contract(t *testing.T) {
	storeContract(t, func(t *testing.T) Store {
		t.Helper()
		path := t.TempDir() + "/contract.db"
		s, err := OpenLibSQLConfig(LibSQLConfig{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}
