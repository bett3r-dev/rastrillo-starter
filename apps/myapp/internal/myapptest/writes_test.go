package myapptest

import (
	"errors"
	"slices"
	"testing"

	myapp "myapp/internal/myapp"
	"myproject/libs/changes"
	"myproject/libs/telemetry"
)

// The allow-list (myapp.ReasonWrites, changes.Policy.Allow) holds
// itself to the vocabulary: every declared reason says what it writes,
// only on its own context's tables, and only columns those tables
// have. That the scenarios land nothing outside it is every scenario
// passing: a write outside it is refused with ErrReasonMismatch.
func TestEveryReasonDeclaresWhatItWrites(t *testing.T) {
	t.Parallel()
	_, d := newApp(t)
	for _, r := range myapp.Reasons {
		w, ok := myapp.ReasonWrites[r]
		if !ok || len(w) == 0 {
			t.Errorf("%s declares no writes in its reasons file", r)
			continue
		}
		for table, cols := range w {
			if got, want := myapp.ContextOfTable(table), myapp.ContextOfReason(r); got != want {
				t.Errorf("%s (%s) declares writes on %s (%s)", r, want, table, got)
			}
			var have []string
			if err := d.G.Raw("SELECT name FROM pragma_table_info(?)", table).Scan(&have).Error; err != nil {
				t.Fatal(err)
			}
			for _, c := range cols {
				if !slices.Contains(have, c) {
					t.Errorf("%s declares %s.%s, which is not a column", r, table, c)
				}
			}
		}
	}
	for r := range myapp.ReasonWrites {
		if myapp.ContextOfReason(r) == "" {
			t.Errorf("writes declared for %s, which is not a reason", r)
		}
	}
}

// The wrong reason from the right context (gastos's mutant M8b): a
// correction that also moves who wrote the note passes the context
// checks, since NoteCorrected is the notes context's reason on the
// notes table, and only the allow-list refuses it. Nothing lands.
func TestACorrectionThatMovesTheAuthorIsRefused(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	luis := s.aMember("Luis")
	ana.writesNote("Groceries", "milk")
	n := s.note("Groceries")
	s.mark_()

	ctx, span := telemetry.Start(t.Context(), "a correction that moves the author")
	defer span.End()
	tr := changes.Begin("member:"+itoa(ana.id())).Within("note:"+itoa(n.ID)).Because(myapp.ReasonNoteCorrected, "")
	err := s.db.G.WithContext(tr.Context(ctx)).Model(&n).
		Updates(map[string]any{"Title": "Groceries!", "AuthorID": luis.id()}).Error
	if !errors.Is(err, changes.ErrReasonMismatch) {
		t.Fatalf("err = %v, want ErrReasonMismatch", err)
	}
	if after := s.note("Groceries"); after.AuthorID != ana.id() {
		t.Fatalf("the refused write landed: author %d", after.AuthorID)
	}
	s.changed()
}
