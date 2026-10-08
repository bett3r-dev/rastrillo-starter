// Package icons is this app's icon set, scaffolded once by
// rastrillo new --icons=lucide --icon-delivery=inline and owned
// by this app from then on. Edit it, add to it, or replace it.
//
// Keys are Lucide slugs whatever the set: that is rastrillo's icon
// vocabulary, so {{icon "search"}} means the same thing in every
// app and the shipped ui/ partials never have to change.
//
// Wire both seams onto your template tree:
//
//	tmpl := template.Must(template.New("").
//	        Funcs(ui.Funcs(ui.WithIcons(icons.Icon, icons.Assets))).
//	        ParseFS(ui.Templates(), "*.html"))
//
// Every icon is aria-hidden: it sits beside its own visible text label,
// which is the accessible name. A control that uses an icon as its ONLY
// label needs an explicit aria-label.
package icons

import "html/template"

var icons = map[string]template.HTML{
	"history":        `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l4 2"/></svg>`,
	"log-out":        `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" x2="9" y1="12" y2="12"/></svg>`,
	"pencil":         `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"/><path d="m15 5 4 4"/></svg>`,
	"alert-triangle": `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3Z"/><path d="M12 9v4"/><path d="M12 17h.01"/></svg>`,
	"check":          `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><path d="M20 6 9 17l-5-5"/></svg>`,
	"check-circle":   `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><path d="m9 12 2 2 4-4"/></svg>`,
	"chevron-down":   `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><path d="m6 9 6 6 6-6"/></svg>`,
	"help-circle":    `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><path d="M12 17h.01"/></svg>`,
	"info":           `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/></svg>`,
	"kebab":          `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><circle cx="12" cy="12" r="1"/><circle cx="12" cy="5" r="1"/><circle cx="12" cy="19" r="1"/></svg>`,
	"menu":           `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><path d="M4 12h16"/><path d="M4 6h16"/><path d="M4 18h16"/></svg>`,
	"plus":           `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><path d="M5 12h14"/><path d="M12 5v14"/></svg>`,
	"search":         `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>`,
	"x":              `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>`,
	"x-circle":       `<svg class="icon" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><path d="m15 9-6 6"/><path d="m9 9 6 6"/></svg>`,
}

// Icon renders one icon by its Lucide slug. An unknown slug renders
// nothing rather than panicking a page mid-response — a typo costs a
// missing icon, not a crash. rastrillo generate --check catches it
// before ship.
func Icon(slug string) template.HTML { return icons[slug] }

// Assets is the markup this delivery mode needs in the document <head>.
// Inline delivery vendors every icon into the binary, so there is
// nothing to load and this returns empty. Call it anyway: switching
// --icon-delivery later then needs no template change.
func Assets() template.HTML { return "" }
