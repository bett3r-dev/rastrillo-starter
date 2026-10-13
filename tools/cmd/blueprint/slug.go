package main

import (
	"regexp"
	"strings"
)

// Slugify, NodeKey and nodeAbbrev are an exact copy of blueprint-go's
// (../blueprint-go-non-ddd/apps/server/internal/server/design/vocabulary.go:30-37
// and :107-131), itself graph.ts's slugify and nodeId: an id built
// here is the id blueprint-go builds for the same node. Change them
// only together with that file; TestKeysAreTheOldIds runs its cases.

// nodeAbbrev is each type's abbreviation, the middle of a node's key
// (NODE_ABBREV).
var nodeAbbrev = map[string]string{
	"aggregate": "agg", "system": "sys", "command": "cmd", "event": "evt",
	"policy": "pol", "read-model": "rm", "external-system": "ext",
	"datastore": "tbl", "cache": "cache", "ui": "ui", "postgres-view": "view",
	"invariant": "inv",
}

var (
	slugLowerUpper = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	slugUpperRun   = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	slugOther      = regexp.MustCompile(`[^a-zA-Z0-9]+`)
	slugEdges      = regexp.MustCompile(`^-+|-+$`)
)

// Slugify is graph.ts's slugify: words split at case changes and at
// anything not a letter or digit, joined by '-', lowercase.
func Slugify(label string) string {
	s := slugLowerUpper.ReplaceAllString(label, "$1-$2")
	s = slugUpperRun.ReplaceAllString(s, "$1-$2")
	s = slugOther.ReplaceAllString(s, "-")
	s = slugEdges.ReplaceAllString(s, "")
	return strings.ToLower(s)
}

// NodeKey is a node's key, the old node id (nodeId,
// proposedNodeId): "<subdomain slug>_<type abbreviation>_<label slug>",
// so "Checkout", "command", "Place Order" is
// "checkout_cmd_place-order". Unique within a session (F15) and never
// changed, whatever later changes its label.
func NodeKey(subdomain, nodeType, label string) string {
	return Slugify(subdomain) + "_" + nodeAbbrev[nodeType] + "_" + Slugify(label)
}
