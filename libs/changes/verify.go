package changes

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Mismatch is one place the change record and the live data
// disagree: a row the fold says should look one way and does not, a
// row the fold says is gone and is not, or a row with no history.
type Mismatch struct {
	Entity   string
	EntityID int64
	Column   string // empty when the whole row is the problem
	Folded   string
	Live     string
	Problem  string
}

func (m Mismatch) String() string {
	if m.Column == "" {
		return fmt.Sprintf("%s #%d: %s", m.Entity, m.EntityID, m.Problem)
	}
	return fmt.Sprintf("%s #%d.%s: %s (folded %s, live %s)", m.Entity, m.EntityID, m.Column, m.Problem, m.Folded, m.Live)
}

// Verify folds every tracked row's changes in id order and compares
// the result with the row as it is: the whole row from its create,
// each update's fields over it, gone after a delete. It names the
// rows that disagree, in table order then id order. exempt columns
// (the password hash) and updated_at, which GORM stamps outside the
// diff, are not compared.
//
// It also holds each table's changes, in id order after its rows, to
// the rules in invariants: a change naming a rule no invariant
// declares, at any age, and a change whose (reason, table) a rule
// shapes that names none, but only when it was created after the
// ledger's applied_at for RuleMigration, since a record from before
// the rule column is not held to name one (RUS-83 D11). So a database
// that has not run RuleMigration (its ledger has no row for it, or
// changes has no rule column) holds no change to name a rule; one
// with the column and no ledger at all (built by AutoMigrate) holds
// every change.
//
// This is the check after the fact: a write that met neither the
// hook nor the guard trigger (a migration, another process) shows
// up here as a row the record cannot explain.
func Verify(g *gorm.DB, tracked []string, exempt map[string][]string, invariants []Invariant) ([]Mismatch, error) {
	var out []Mismatch
	naming := schema.NamingStrategy{}
	cutoff, holds, err := ruleCutoff(g)
	if err != nil {
		return nil, err
	}
	p := Policy{Invariants: invariants}
	declared := map[string]bool{}
	for _, inv := range invariants {
		declared[inv.Name] = true
	}
	for _, table := range tracked {
		skip := map[string]bool{"updated_at": true}
		for _, c := range exempt[table] {
			skip[c] = true
		}
		var cs []Change
		if err := g.Where("entity = ?", table).Order("id").Find(&cs).Error; err != nil {
			return nil, err
		}
		type state struct {
			cols    map[string]any
			deleted bool
		}
		folded := map[int64]*state{}
		for _, c := range cs {
			st := folded[c.EntityID]
			if st == nil {
				st = &state{cols: map[string]any{}}
				folded[c.EntityID] = st
			}
			switch c.Op {
			case "delete":
				st.deleted = true
				continue
			case "create":
				st.cols = map[string]any{}
				st.deleted = false
			}
			var diff map[string]any
			if err := json.Unmarshal([]byte(c.Diff), &diff); err != nil {
				out = append(out, Mismatch{Entity: table, EntityID: c.EntityID, Problem: fmt.Sprintf("change #%d diff is not an object: %v", c.ID, err)})
				continue
			}
			for k, v := range diff {
				st.cols[naming.ColumnName("", k)] = v
			}
		}
		var live []map[string]any
		if err := g.Table(table).Order("id").Find(&live).Error; err != nil {
			return nil, err
		}
		seen := map[int64]bool{}
		for _, row := range live {
			id := asInt(row["id"])
			seen[id] = true
			st, ok := folded[id]
			if !ok {
				out = append(out, Mismatch{Entity: table, EntityID: id, Problem: "live row with no change record"})
				continue
			}
			if st.deleted {
				out = append(out, Mismatch{Entity: table, EntityID: id, Problem: "deleted in the record, still live"})
				continue
			}
			cols := make([]string, 0, len(st.cols))
			for c := range st.cols {
				cols = append(cols, c)
			}
			sort.Strings(cols)
			for _, c := range cols {
				if skip[c] {
					continue
				}
				lv, has := row[c]
				if !has {
					out = append(out, Mismatch{Entity: table, EntityID: id, Column: c, Problem: "column in the record, not in the table"})
					continue
				}
				f, l := norm(st.cols[c]), norm(lv)
				if f != l {
					out = append(out, Mismatch{Entity: table, EntityID: id, Column: c, Folded: f, Live: l, Problem: "disagree"})
				}
			}
		}
		ids := make([]int64, 0, len(folded))
		for id := range folded {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			if !seen[id] && !folded[id].deleted {
				out = append(out, Mismatch{Entity: table, EntityID: id, Problem: "in the record, not live, and never deleted"})
			}
		}
		for _, c := range cs {
			switch shaping := p.shaping(c.Reason, table); {
			case c.Rule != "" && !declared[c.Rule]:
				out = append(out, Mismatch{Entity: table, EntityID: c.EntityID, Problem: fmt.Sprintf("change #%d names rule %s, which no invariant declares", c.ID, c.Rule)})
			case c.Rule == "" && len(shaping) > 0 && holds && c.CreatedAt.After(cutoff):
				out = append(out, Mismatch{Entity: table, EntityID: c.EntityID, Problem: fmt.Sprintf("change #%d is %s, which %s shapes, and names no rule", c.ID, c.Reason, strings.Join(shaping, ", "))})
			}
		}
	}
	return out, nil
}

// RuleMigration is the ledger id (rastrillo_migrations.id, namespace
// "/" file name) of the migration that added changes.rule.
const RuleMigration = "changes/0005_changes_rule"

// ruleCutoff is when RuleMigration was applied, as the ledger records
// it (RFC 3339 in UTC), and whether a change after it is held to name
// its rule at all. Without the rule column, or with a ledger that has
// no RuleMigration row, nothing is held: every record predates the
// column. With the column and no ledger, every change is held (the
// zero time).
func ruleCutoff(g *gorm.DB) (time.Time, bool, error) {
	if !g.Migrator().HasColumn("changes", "rule") {
		return time.Time{}, false, nil
	}
	if !g.Migrator().HasTable("rastrillo_migrations") {
		return time.Time{}, true, nil
	}
	var at []string
	if err := g.Raw("SELECT applied_at FROM rastrillo_migrations WHERE id = ?", RuleMigration).Scan(&at).Error; err != nil {
		return time.Time{}, false, err
	}
	if len(at) == 0 {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339Nano, at[0])
	if err != nil {
		return time.Time{}, false, fmt.Errorf("changes: ledger applied_at for %s: %w", RuleMigration, err)
	}
	return t, true, nil
}

func asInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

// norm renders a value from either side so equal facts compare
// equal: integers without a decimal point, times as one instant in
// UTC, NULL and the empty string alike.
func norm(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case string:
		if t, ok := parseTime(x); ok {
			return t.UTC().Format(time.RFC3339Nano)
		}
		return x
	case bool:
		if x {
			return "1"
		}
		return "0"
	}
	return fmt.Sprintf("%v", v)
}

var timeLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"}

func parseTime(s string) (time.Time, bool) {
	if len(s) < 19 || !strings.HasPrefix(s[4:5], "-") {
		return time.Time{}, false
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
