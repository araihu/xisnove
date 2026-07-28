package security

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func TestLoginCSRFCookieIsHostBoundAndTamperEvident(t *testing.T) {
	manager, err := NewCookieManager(testSecret, true, bytes.NewReader(bytes.Repeat([]byte{0x2a}, 64)))
	if err != nil {
		t.Fatalf("new cookie manager: %v", err)
	}
	recorder := httptest.NewRecorder()

	token, err := manager.IssueLoginCSRF(recorder)
	if err != nil {
		t.Fatalf("issue login CSRF: %v", err)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookie count = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "__Host-xisnove-login-csrf" {
		t.Fatalf("cookie name = %q", cookie.Name)
	}
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie flags = Secure:%t HttpOnly:%t SameSite:%v", cookie.Secure, cookie.HttpOnly, cookie.SameSite)
	}
	if cookie.Path != "/" || cookie.Domain != "" {
		t.Fatalf("cookie scope = path %q domain %q", cookie.Path, cookie.Domain)
	}

	request := httptest.NewRequest(http.MethodPost, "https://ui.example.test/login", nil)
	request.AddCookie(cookie)
	if !manager.ValidateLoginCSRF(request, token) {
		t.Fatal("issued CSRF token did not validate")
	}
	if manager.ValidateLoginCSRF(request, token+"tampered") {
		t.Fatal("tampered CSRF token validated")
	}
}

func TestSessionCookieRoundTripsOpaqueCredentialWithoutPlaintext(t *testing.T) {
	manager, err := NewCookieManager(testSecret, true, bytes.NewReader(bytes.Repeat([]byte{0x2a}, 64)))
	if err != nil {
		t.Fatalf("new cookie manager: %v", err)
	}
	recorder := httptest.NewRecorder()

	manager.SetSession(recorder, "opaque.control-plane/credential")
	if strings.Contains(recorder.Header().Get("Set-Cookie"), "opaque.control-plane/credential") {
		t.Fatal("Set-Cookie exposes the plaintext control-plane credential")
	}
	cookie := recorder.Result().Cookies()[0]
	if cookie.Name != "__Host-xisnove-session" {
		t.Fatalf("cookie name = %q", cookie.Name)
	}
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags = Secure:%t HttpOnly:%t SameSite:%v", cookie.Secure, cookie.HttpOnly, cookie.SameSite)
	}
	if cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Fatalf("unexpected persistent or broad cookie: %#v", cookie)
	}

	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/", nil)
	request.AddCookie(cookie)
	credential, ok := manager.Session(request)
	if !ok || credential != "opaque.control-plane/credential" {
		t.Fatalf("session = %q, %t", credential, ok)
	}
}

func TestSessionCSRFIsBoundToOpaqueCredential(t *testing.T) {
	manager, err := NewCookieManager(testSecret, true, bytes.NewReader(bytes.Repeat([]byte{0x2a}, 64)))
	if err != nil {
		t.Fatalf("new cookie manager: %v", err)
	}

	token := manager.SessionCSRF("session-a")
	if token == "" || token == "session-a" {
		t.Fatalf("unsafe CSRF token %q", token)
	}
	if !manager.ValidateSessionCSRF("session-a", token) {
		t.Fatal("session CSRF token did not validate")
	}
	if manager.ValidateSessionCSRF("session-b", token) {
		t.Fatal("session CSRF token validated for another credential")
	}
}

func TestClearSessionExpiresCookieWithOriginalSecurityFlags(t *testing.T) {
	manager, err := NewCookieManager(testSecret, true, bytes.NewReader(bytes.Repeat([]byte{0x2a}, 64)))
	if err != nil {
		t.Fatalf("new cookie manager: %v", err)
	}
	recorder := httptest.NewRecorder()

	manager.ClearSession(recorder)
	cookie := recorder.Result().Cookies()[0]
	if cookie.MaxAge >= 0 || cookie.Expires.IsZero() {
		t.Fatalf("clear cookie MaxAge=%d Expires=%v", cookie.MaxAge, cookie.Expires)
	}
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("clear cookie lost security flags: %#v", cookie)
	}
}

func TestInsecureDevelopmentModeCannotEmitHostPrefix(t *testing.T) {
	manager, err := NewCookieManager(testSecret, false, bytes.NewReader(bytes.Repeat([]byte{0x2a}, 64)))
	if err != nil {
		t.Fatalf("new cookie manager: %v", err)
	}
	recorder := httptest.NewRecorder()
	manager.SetSession(recorder, "credential")

	cookie := recorder.Result().Cookies()[0]
	if cookie.Secure || strings.HasPrefix(cookie.Name, "__Host-") {
		t.Fatalf("development cookie = %#v", cookie)
	}
}

func TestCookieManagerRejectsShortSecret(t *testing.T) {
	if _, err := NewCookieManager([]byte("short"), true, bytes.NewReader(nil)); err == nil {
		t.Fatal("short HMAC secret was accepted")
	}
}
