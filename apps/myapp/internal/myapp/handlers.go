package myapp

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"amadan.net/rastrillo/rastrillo/db"
	"amadan.net/rastrillo/rastrillo/flash"
	"amadan.net/rastrillo/rastrillo/form"
	"amadan.net/rastrillo/rastrillo/password"
	"amadan.net/rastrillo/rastrillo/sessions"

	"myproject/libs/changes"
)

// app holds what every handler needs.
type app struct {
	db     *db.DB
	logger *slog.Logger
	sess   *sessions.Sessions
}

// trail opens the request's change Trail with its actor and its entry
// point: the business name of the command the person gave
// (WriteNote), written inline by the handler that opens it. Left "",
// the change hook records the request's method and route pattern
// instead. Every change the Trail lands carries it (docs/flow.md). The
// correlation is not handed in: each write reads it off the context
// it runs under. The actor is the signed-in member (member:<id>), or
// "human" on the one route a person uses before they are a member
// (signing up). Agents open their own with their name.
func (a *app) trail(r *http.Request, entryPoint string) (*changes.Trail, context.Context) {
	actor := "human"
	if uid, ok := sessions.UserID(r); ok {
		actor = "member:" + strconv.FormatInt(uid, 10)
	}
	t := changes.Begin(actor)
	t.EntryPoint = entryPoint
	return t, t.Context(r.Context())
}

// fail logs the error under the request's correlation id and renders
// the error page. The ref a person sees is that id, so the log line
// and the change records of the same request share one handle.
func (a *app) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	id := Correlation(r.Context())
	a.logger.ErrorContext(r.Context(), what, "err", err)
	ErrorPage(w, r, http.StatusInternalServerError, id)
}

// member is the signed-in Member, loaded per request.
func (a *app) member(r *http.Request) (Member, bool) {
	uid, ok := sessions.UserID(r)
	if !ok {
		return Member{}, false
	}
	var m Member
	if err := a.db.G.WithContext(r.Context()).First(&m, uid).Error; err != nil {
		return Member{}, false
	}
	return m, true
}

// --- identity -----------------------------------------------------

func (a *app) lookupMember(ctx context.Context, email string) (int64, string, error) {
	var m Member
	err := a.db.G.WithContext(ctx).Where("email = ?", email).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, "", sql.ErrNoRows
	}
	return m.ID, m.PasswordHash, err
}

// createMember is joining: the password plugin's Create callback,
// explained as MemberJoined. It is also the example's cross-context
// policy: the notes context reacts to the join by writing a welcome
// note, a change caused by the MemberJoined change, in the same
// transaction (MemberJoined[accounts] ⟹ WelcomeNoteWritten[notes] in
// docs/flow.md). The accounts code never names a notes table's
// reason as its own; it causes one.
func (a *app) createMember(ctx context.Context, email, hash string) (int64, error) {
	name, _ := ctx.Value(nameKey{}).(string)
	if name == "" {
		name = strings.SplitN(email, "@", 2)[0]
	}
	t := changes.Begin("human").Because(ReasonMemberJoined, "")
	t.EntryPoint = "Join"
	m := Member{Email: email, Name: name}
	// The diff must not carry the hash: write it in a second step,
	// exempt from the record (TrackedExempt), so the MemberJoined
	// change shows who joined and nothing secret. Same transaction.
	err := a.db.G.WithContext(t.Context(ctx)).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		if err := tx.Session(&gorm.Session{SkipHooks: true}).
			Exec("UPDATE members SET password_hash = ? WHERE id = ?", hash, m.ID).Error; err != nil {
			return err
		}
		joined, _ := t.Last()
		return welcome(tx, t, m, joined)
	})
	return m.ID, err
}

type nameKey struct{}

// withSignupName carries the form's display name to createMember,
// which the plugin calls with the context alone.
func withSignupName(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.PostFormValue("name"))
		next(w, r.WithContext(context.WithValue(r.Context(), nameKey{}, name)))
	}
}

func (a *app) renderSignin(w http.ResponseWriter, r *http.Request, d password.PageData) {
	a.render(w, r, "signin", "Sign in", d)
}

func (a *app) renderSignup(w http.ResponseWriter, r *http.Request, d password.PageData) {
	a.render(w, r, "signup", "Join", d)
}

// --- notes --------------------------------------------------------

type noteRow struct {
	ID     int64
	Title  string
	Body   string
	Author string
}

// notes lists every note, newest first. The author's name is a
// lookup by id within the accounts context's table, never a join
// across contexts (make contexts).
func (a *app) notes(w http.ResponseWriter, r *http.Request) {
	var ns []Note
	if err := a.db.G.WithContext(r.Context()).Order("id DESC").Find(&ns).Error; err != nil {
		a.fail(w, r, "notes", err)
		return
	}
	var ms []Member
	if err := a.db.G.WithContext(r.Context()).Find(&ms).Error; err != nil {
		a.fail(w, r, "notes", err)
		return
	}
	names := map[int64]string{}
	for _, m := range ms {
		names[m.ID] = m.Name
	}
	rows := make([]noteRow, 0, len(ns))
	for _, n := range ns {
		rows = append(rows, noteRow{ID: n.ID, Title: n.Title, Body: n.Body, Author: names[n.AuthorID]})
	}
	a.render(w, r, "notes", "Notes", rows)
}

type noteForm struct {
	ID     int64
	Values map[string]string
	Errors form.Errors
}

var noteFields = []form.Field{
	{Name: "title", Required: true},
	{Name: "body"},
}

func (a *app) noteNew(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "note_new", "Write a note", noteForm{Values: map[string]string{}, Errors: form.Errors{}})
}

func (a *app) noteCreate(w http.ResponseWriter, r *http.Request) {
	me, ok := a.member(r)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p := form.Parse(r, noteFields...)
	if !p.OK() {
		w.WriteHeader(http.StatusUnprocessableEntity)
		a.render(w, r, "note_new", "Write a note", noteForm{Values: p.Echo(), Errors: p.Errors()})
		return
	}
	t, ctx := a.trail(r, "WriteNote")
	t.Because(ReasonNoteWritten, "")
	n := Note{AuthorID: me.ID, Title: p.String("title"), Body: p.String("body")}
	if err := a.db.G.WithContext(ctx).Create(&n).Error; err != nil {
		a.fail(w, r, "write note", err)
		return
	}
	flash.Set(w, "notice", "Wrote "+n.Title+".")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// welcome is the notes context's policy on a member joining: a note
// greeting them, caused by the MemberJoined change it reacts to.
func welcome(tx *gorm.DB, t *changes.Trail, m Member, joined changes.Change) error {
	t.Because(ReasonWelcomeNoteWritten, joined.Ref())
	return tx.Create(&Note{AuthorID: m.ID, Title: "Welcome, " + m.Name}).Error
}

// note loads the note in the URL; a missing one is a 404.
func (a *app) note(w http.ResponseWriter, r *http.Request) (Note, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return Note{}, false
	}
	var n Note
	err = a.db.G.WithContext(r.Context()).First(&n, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		http.NotFound(w, r)
		return Note{}, false
	}
	if err != nil {
		a.fail(w, r, "load note", err)
		return Note{}, false
	}
	return n, true
}

func (a *app) noteCorrectNew(w http.ResponseWriter, r *http.Request) {
	n, ok := a.note(w, r)
	if !ok {
		return
	}
	a.render(w, r, "note_correct", "Correct a note", noteForm{ID: n.ID,
		Values: map[string]string{"title": n.Title, "body": n.Body}, Errors: form.Errors{}})
}

// noteCorrect puts a note right: a new change, NoteCorrected, whose
// cause is the most recent change on that note, so the correction
// sits one click from what it corrects on /changes. The row's fields
// are written forward; the record keeps both versions.
func (a *app) noteCorrect(w http.ResponseWriter, r *http.Request) {
	n, ok := a.note(w, r)
	if !ok {
		return
	}
	p := form.Parse(r, noteFields...)
	if !p.OK() {
		w.WriteHeader(http.StatusUnprocessableEntity)
		a.render(w, r, "note_correct", "Correct a note", noteForm{ID: n.ID, Values: p.Echo(), Errors: p.Errors()})
		return
	}
	var last changes.Change
	if err := a.db.G.WithContext(r.Context()).Where("entity = ? AND entity_id = ?", "notes", n.ID).
		Order("id DESC").First(&last).Error; err != nil {
		a.fail(w, r, "correct note", err)
		return
	}
	t, ctx := a.trail(r, "CorrectNote")
	t.Because(ReasonNoteCorrected, last.Ref())
	if err := a.db.G.WithContext(ctx).Model(&n).Select("Title", "Body").
		Updates(map[string]any{"Title": p.String("title"), "Body": p.String("body")}).Error; err != nil {
		a.fail(w, r, "correct note", err)
		return
	}
	flash.Set(w, "notice", "Corrected "+p.String("title")+".")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- the change log -----------------------------------------------

type changeRow struct {
	changes.Change
	When      string
	CauseHref string // "#change-12" when the cause is an earlier change, so a correction sits one click from what it corrects
}

// changeLog is the audit trail as a screen: the same rows a 3 a.m.
// `SELECT reason, cause FROM changes` would read, newest first.
func (a *app) changeLog(w http.ResponseWriter, r *http.Request) {
	var cs []changes.Change
	if err := a.db.G.WithContext(r.Context()).Order("id DESC").Limit(200).Find(&cs).Error; err != nil {
		a.fail(w, r, "changes", err)
		return
	}
	rows := make([]changeRow, 0, len(cs))
	for _, c := range cs {
		row := changeRow{Change: c, When: c.CreatedAt.UTC().Format(time.RFC3339)}
		if strings.HasPrefix(c.Cause, "change:") {
			row.CauseHref = "#change-" + strings.TrimPrefix(c.Cause, "change:")
		}
		rows = append(rows, row)
	}
	a.render(w, r, "changes", "What happened", rows)
}
