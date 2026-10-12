package myapptest

import (
	"encoding/json"
	"net/http"
	"testing"

	myapp "myapp/internal/myapp"
)

// Given a member who wrote and corrected a note, When she asks for
// what happened since a mark as JSON, Then she gets the same records
// the screen shows, oldest first, each field as the contract names it;
// and nobody signed out reads them.
func TestScenarioTheChangeLogIsReadableByAMachine(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	s.mark_()
	since := s.mark
	redirected(t, ana.writesNote("Groceires", "Milk"), "/")
	redirected(t, ana.correctsNote(s.note("Groceires").ID, "Groceries", "Milk"), "/")
	want := s.changed(myapp.ReasonNoteWritten, myapp.ReasonNoteCorrected)

	rec := ana.get("/changes.json?after=" + itoa(since))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var feed struct {
		Changes []struct {
			ID            int64           `json:"id"`
			Reason        string          `json:"reason"`
			Cause         string          `json:"cause"`
			RequestID     string          `json:"request_id"`
			CorrelationID string          `json:"correlation_id"`
			Actor         string          `json:"actor"`
			OnBehalfOf    *string         `json:"on_behalf_of"`
			EntryPoint    string          `json:"entry_point"`
			Scope         string          `json:"scope"`
			Table         string          `json:"table"`
			RowID         int64           `json:"row_id"`
			Op            string          `json:"op"`
			Diff          json.RawMessage `json:"diff"`
			CreatedAt     string          `json:"created_at"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, rec.Body.String())
	}
	if len(feed.Changes) != len(want) {
		t.Fatalf("got %d changes, want %d\n%s", len(feed.Changes), len(want), rec.Body.String())
	}
	for i, c := range feed.Changes {
		w := want[i]
		if c.ID != w.ID || c.Reason != w.Reason || c.Cause != w.Cause || c.RequestID != w.RequestID ||
			c.CorrelationID != w.CorrelationID || c.Actor != w.Actor || c.OnBehalfOf == nil || *c.OnBehalfOf != w.OnBehalfOf ||
			c.EntryPoint != w.EntryPoint || c.Scope != w.Scope || c.Table != w.Entity || c.RowID != w.EntityID ||
			c.Op != w.Op || string(c.Diff) != w.Diff || c.CreatedAt == "" {
			t.Fatalf("change %d:\n  feed %+v\n  db   %+v", i, c, w)
		}
	}
	// A page ends where the next begins: after the last id, nothing.
	if rec := ana.get("/changes.json?after=" + itoa(want[len(want)-1].ID)); rec.Body.String() != "{\"changes\":[]}\n" {
		t.Fatalf("after the last change: %s", rec.Body.String())
	}
	if rec := ana.get("/changes.json?after=x"); rec.Code != http.StatusBadRequest {
		t.Fatalf("after=x: status %d", rec.Code)
	}
	// And nobody signed out reads it
	if rec := get(t, s.h, "/changes.json"); rec.Code == http.StatusOK {
		t.Fatalf("signed-out read of the change feed: status %d", rec.Code)
	}
}
