package parchment_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/dpopsuev/parchment"
	_ "modernc.org/sqlite"
)

func TestCreateArtifact_RejectsWrongDomainStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	_, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Title:  "wrong domain status",
		Labels: []string{"kind:effort.task", "note.fleeting", "priority:medium"},
		Sections: []parchment.Section{
			{Name: "context", Text: "c"},
		},
	})
	if err == nil {
		t.Fatal("expected create with note.fleeting on effort.task to fail")
	}
}

func TestCreateArtifact_AllowsCancelled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Title:  "cancel ok",
		Labels: []string{"kind:effort.task", "status:cancelled", "priority:medium"}, //nolint:misspell // British spelling matches stored status
		Sections: []parchment.Section{
			{Name: "context", Text: "c"},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if parchment.StatusFromLabels(art.Labels) != "cancelled" { //nolint:misspell // British spelling matches stored status
		t.Fatalf("status=%q", parchment.StatusFromLabels(art.Labels))
	}
}

func TestSetField_LabelsCannotSmuggleStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Title:  "labels smuggle",
		Labels: []string{"kind:effort.task", "priority:medium"},
		Sections: []parchment.Section{{Name: "context", Text: "c"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := proto.SetField(ctx, []string{art.ID}, "labels", "kind:effort.task,note.fleeting")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].OK {
		t.Fatal("expected labels status smuggle to fail")
	}
}

func TestSchemaEvolution_PreservesArtifactsAcrossOpens(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "t.sqlite")
	ctx := context.Background()

	st, err := parchment.OpenSQLite(path)
	if err != nil {
		t.Fatalf("open1: %v", err)
	}
	proto := parchment.New(st, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Title:  "persist",
		Labels: []string{"kind:effort.task", "priority:low"},
		Sections: []parchment.Section{{Name: "context", Text: "c"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	st2, err := parchment.OpenSQLite(path)
	if err != nil {
		t.Fatalf("open2: %v", err)
	}
	defer st2.Close()
	got, err := st2.Get(ctx, art.ID)
	if err != nil || got == nil {
		t.Fatalf("artifact lost after reopen: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ver int
	if err := db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		t.Fatal(err)
	}
	if ver != parchment.SchemaUserVersion {
		t.Fatalf("user_version=%d want %d", ver, parchment.SchemaUserVersion)
	}
	var aliasCols int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('artifacts') WHERE name IN ('uid','alias')").Scan(&aliasCols); err != nil {
		t.Fatal(err)
	}
	if aliasCols != 0 {
		t.Fatalf("legacy columns still present: %d", aliasCols)
	}
}

func TestSchemaEvolution_RejectsNewerUserVersion(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "newerver.sqlite")

	st, err := parchment.OpenSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = st.Close()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 9999"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	_, err = parchment.OpenSQLite(path)
	if err == nil {
		t.Fatal("expected open to fail for newer user_version")
	}
}
