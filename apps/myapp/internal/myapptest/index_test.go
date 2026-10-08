package myapptest

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	myapp "myapp/internal/myapp"
)

// hashedStylesheet is the fingerprinted URL shape the index page
// links: /static/tokens.<16 hex>.css.
var hashedStylesheet = regexp.MustCompile(`/static/tokens\.[0-9a-f]{16}\.css`)

// These two tests are about the PLACEHOLDER index page, and they are
// yours to rewrite. They ship because a scaffold whose suite asserts
// nothing teaches nothing, not because "/" answering 200 with those
// words in it is a property of your app.
//
// The first feature that breaks them is sign-in, which is also the
// first feature most apps add: put "/" in a Group behind
// sessions.Require and a signed-out GET is a 303 to SigninPath, so
// the status assertion fails and the redirect's body carries neither
// the heading nor a stylesheet link. That is the app working. Point
// them at a page that is public in your app, or delete them — the
// rest of this file (the asset URLs, the error page) is about the
// framework's behaviour rather than the placeholder's, and keeps
// earning its place.
//
// Said in the failure messages too, and not only here. The reader who
// needs it is looking at a red gate, and the gate prints messages
// rather than source.
//
// Each test's first statement is t.Parallel(): anything before it runs
// serially, and a fixture built there is held for the whole run.
func TestIndexRenders(t *testing.T) {
	t.Parallel()
	rec := get(t, newApp(t), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: status %d, want 200. If you have put %q behind sign-in this is the placeholder test, not a fault: a signed-out GET redirects, and this test is yours to repoint or delete", rec.Code, "/")
	}
	if !strings.Contains(rec.Body.String(), "this is a rastrillo app") {
		t.Errorf("GET /: page is missing its heading. If you have replaced the placeholder index this test is yours to rewrite:\n%s", rec.Body.String())
	}
}

func TestIndexLinksFingerprintedStylesheet(t *testing.T) {
	t.Parallel()
	h := newApp(t)
	href := hashedStylesheet.FindString(get(t, h, "/").Body.String())
	if href == "" {
		t.Fatal("index page links no fingerprinted stylesheet. If you have put \"/\" behind sign-in, the redirect body has no <link> in it and this test wants pointing at a page that is still public")
	}
	rec := get(t, h, href)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", href, rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("GET %s: Cache-Control = %q, want immutable year", href, got)
	}
}

// The bare name keeps working, just never long-cached — so a changed
// file always shows on an ordinary reload.
func TestBareAssetNameStaysFresh(t *testing.T) {
	t.Parallel()
	rec := get(t, newApp(t), "/static/tokens.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/tokens.css: status %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
}

// ErrorPage is what main.go points Options.ErrorPage at (and the seam
// to wire into Ctx.ErrorPage as handlers grow); called directly, it
// pins that a panic's page is the framework's error-page partial, not
// a stray net/http default.
func TestErrorPageRendersFrameworkPartial(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	myapp.ErrorPage(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusInternalServerError, "abc123")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("ErrorPage: status %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Something went wrong on our side") {
		t.Errorf("ErrorPage body missing the 500 title:\n%s", body)
	}
	if !strings.Contains(body, "abc123") {
		t.Errorf("ErrorPage body missing the ref:\n%s", body)
	}
}
