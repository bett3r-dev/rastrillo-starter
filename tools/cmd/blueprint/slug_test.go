package main

import (
	"os"
	"testing"

	"amadan.net/rastrillo/rastrillo/budget"
)

func TestMain(m *testing.M) { os.Exit(budget.Main(m)) }

// Keys are blueprint-go's, byte for byte: its own cases, copied from
// ../blueprint-go-non-ddd/apps/server/internal/server/design/vocabulary_test.go
// (TestKeysAreTheOldIds). Its EdgeKey case is left out: graph.json's
// edges carry no key, so EdgeKey is not ported.
func TestKeysAreTheOldIds(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, want string }{
		{"Place Order", "place-order"},
		{"placeOrder", "place-order"},
		{"HTTPServer", "http-server"},
		{"  Order -- placed!  ", "order-placed"},
		{"read model v2", "read-model-v2"},
		{"!!!", ""},
	} {
		if got := Slugify(c.in); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := NodeKey("Checkout", "read-model", "Open Orders"); got != "checkout_rm_open-orders" {
		t.Errorf("NodeKey = %q", got)
	}
}
