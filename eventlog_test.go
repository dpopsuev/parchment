package parchment_test

import (
	"context"
	"testing"
	"time"

	"github.com/dpopsuev/parchment"
)

func newEventProto(t *testing.T) (*parchment.Protocol, *parchment.MemoryStore) {
	t.Helper()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, parchment.KnowledgeSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})
	return proto, store
}

func TestEventLog_CreateArtifact_EmitsCreated(t *testing.T) {
	// Given: a Protocol with an in-memory store
	// When: an artifact is created
	// Then: a "created" event is appended to the EventLog for that artifact
	t.Parallel()
	proto, store := newEventProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: parchment.KindTask, Title: "write EventLog tests", Scope: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	events, err := store.GetEvents(ctx, time.Time{}, parchment.EventFilter{ArtifactID: art.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one event after CreateArtifact, got none")
	}
	if events[0].EventType != parchment.EventCreated {
		t.Errorf("first event type = %q, want %q", events[0].EventType, parchment.EventCreated)
	}
	if events[0].ArtifactID != art.ID {
		t.Errorf("event artifact_id = %q, want %q", events[0].ArtifactID, art.ID)
	}
	if events[0].Scope != art.Scope {
		t.Errorf("event scope = %q, want %q", events[0].Scope, art.Scope)
	}
}

func TestEventLog_SetField_EmitsUpdated(t *testing.T) {
	// Given: an existing artifact
	// When: SetField changes a field
	// Then: an "updated" event is appended
	t.Parallel()
	proto, store := newEventProto(t)
	ctx := context.Background()

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: parchment.KindTask, Title: "original title", Scope: "test",
	})

	_, err := proto.SetField(ctx, []string{art.ID}, "title", "new title")
	if err != nil {
		t.Fatal(err)
	}

	events, _ := store.GetEvents(ctx, time.Time{}, parchment.EventFilter{
		ArtifactID: art.ID,
		EventTypes: []string{parchment.EventUpdated},
	})
	if len(events) == 0 {
		t.Fatal("expected updated event after SetField, got none")
	}
}

func TestEventLog_SetStatus_EmitsStatusChanged(t *testing.T) {
	// Given: an active artifact
	// When: SetField changes status
	// Then: a "status_changed" event (not "updated") is appended
	t.Parallel()
	proto, store := newEventProto(t)
	ctx := context.Background()

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: parchment.KindTask, Title: "status change test", Scope: "test",
	})

	_, err := proto.SetField(ctx, []string{art.ID}, "status", "active", parchment.SetFieldOptions{BypassGuards: true})
	if err != nil {
		t.Fatal(err)
	}

	events, _ := store.GetEvents(ctx, time.Time{}, parchment.EventFilter{
		ArtifactID: art.ID,
		EventTypes: []string{parchment.EventStatusChanged},
	})
	if len(events) == 0 {
		t.Fatal("expected status_changed event after SetField(status=active), got none")
	}
}

func TestEventLog_LinkArtifacts_EmitsLinked(t *testing.T) {
	// Given: two artifacts
	// When: LinkArtifacts is called
	// Then: a "linked" event is appended for the source artifact
	t.Parallel()
	proto, store := newEventProto(t)
	ctx := context.Background()

	src, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: parchment.KindTask, Title: "source", Scope: "test",
	})
	dst, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: parchment.KindTask, Title: "destination", Scope: "test",
	})

	_, err := proto.LinkArtifacts(ctx, src.ID, "related", []string{dst.ID})
	if err != nil {
		t.Fatal(err)
	}

	events, _ := store.GetEvents(ctx, time.Time{}, parchment.EventFilter{
		ArtifactID: src.ID,
		EventTypes: []string{parchment.EventLinked},
	})
	if len(events) == 0 {
		t.Fatal("expected linked event after LinkArtifacts, got none")
	}
}

func TestEventLog_GetEvents_FilterByScope(t *testing.T) {
	// Given: artifacts in two different scopes
	// When: GetEvents with scope filter
	// Then: only events from the requested scope are returned
	t.Parallel()
	store := parchment.NewMemoryStore()
	protoA := parchment.New(store, parchment.KnowledgeSchema(), []string{"scope-a"}, nil, parchment.ProtocolConfig{})
	protoB := parchment.New(store, parchment.KnowledgeSchema(), []string{"scope-b"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	_, _ = protoA.CreateArtifact(ctx, parchment.CreateInput{Kind: parchment.KindNote, Title: "in scope-a", Scope: "scope-a"})
	_, _ = protoB.CreateArtifact(ctx, parchment.CreateInput{Kind: parchment.KindNote, Title: "in scope-b", Scope: "scope-b"})

	events, _ := store.GetEvents(ctx, time.Time{}, parchment.EventFilter{Scope: "scope-a"})
	for _, e := range events {
		if e.Scope != "scope-a" {
			t.Errorf("scope filter returned event with scope %q", e.Scope)
		}
	}
	if len(events) == 0 {
		t.Fatal("expected events for scope-a, got none")
	}
}
