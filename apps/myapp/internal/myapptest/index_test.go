package myapptest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	myapp "myapp/internal/myapp"
)

// hashedStylesheet is the fingerprinted URL shape the index page
// links: /static/tokens.<16 hex>.css.
var hashedStylesheet = regexp.MustCompile(`/static/tokens\.[0-9a-f]{16}\.css`)

// "/" is behind sign-in, so the public page is /signin.
func TestSigninRenders(t *testing.T) {
	t.Parallel()
	h, _ := newApp(t)
	rec := get(t, h, "/signin")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /signin: status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Sign in") {
		t.Errorf("GET /signin: page is missing its heading:\n%s", rec.Body.String())
	}
}

func TestSignedOutHomeRedirectsToSignin(t *testing.T) {
	t.Parallel()
	h, _ := newApp(t)
	rec := get(t, h, "/")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/signin") {
		t.Fatalf("GET / signed out: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestSigninLinksFingerprintedStylesheet(t *testing.T) {
	t.Parallel()
	h, _ := newApp(t)
	href := hashedStylesheet.FindString(get(t, h, "/signin").Body.String())
	if href == "" {
		t.Fatal("signin page links no fingerprinted stylesheet")
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
	h, _ := newApp(t)
	rec := get(t, h, "/static/tokens.css")
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

func TestEveryResponseCarriesACorrelationID(t *testing.T) {
	t.Parallel()
	h, _ := newApp(t)
	// A request nobody traced gets a trace of its own: 32 hex digits.
	rec := get(t, h, "/signin")
	if id := rec.Header().Get("X-Correlation-Id"); len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		t.Fatalf("X-Correlation-Id = %q, want a fresh trace id", id)
	}
	// A request made inside someone else's trace joins it.
	req := httptest.NewRequest(http.MethodGet, "/signin", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Correlation-Id"); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("X-Correlation-Id = %q, want the caller's trace id", got)
	}
}

// The dev refresh (make dev, MYAPP_DEV_RELOAD) never reaches a build
// that runs without it: no /dev/boot route, no script on the page.
func TestDevReloadIsOffWithoutTheEnv(t *testing.T) {
	t.Parallel()
	if os.Getenv(myapp.DevReloadEnv) != "" {
		t.Skipf("%s is set in this environment", myapp.DevReloadEnv)
	}
	h, _ := newApp(t)
	if rec := get(t, h, "/dev/boot"); rec.Code == http.StatusOK {
		t.Errorf("GET /dev/boot: 200 with the refresh off")
	}
	if body := get(t, h, "/signin").Body.String(); strings.Contains(body, "devreload") {
		t.Errorf("signin page loads devreload.js with the refresh off")
	}
}
