package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	secureSessionCookie   = "__Host-xisnove-session"
	secureLoginCSRFCookie = "__Host-xisnove-login-csrf"
	devSessionCookie      = "xisnove-session"
	devLoginCSRFCookie    = "xisnove-login-csrf"
	loginCSRFLifetime     = 10 * time.Minute
)

type CookieManager struct {
	secret []byte
	secure bool
	random io.Reader
}

func NewCookieManager(secret []byte, secure bool, random io.Reader) (*CookieManager, error) {
	if len(secret) < sha256.Size {
		return nil, errors.New("cookie HMAC secret must contain at least 32 bytes")
	}
	if random == nil {
		random = rand.Reader
	}
	return &CookieManager{
		secret: append([]byte(nil), secret...),
		secure: secure,
		random: random,
	}, nil
}

func (m *CookieManager) IssueLoginCSRF(w http.ResponseWriter) (string, error) {
	nonce := make([]byte, sha256.Size)
	if _, err := io.ReadFull(m.random, nonce); err != nil {
		return "", err
	}
	encodedNonce := base64.RawURLEncoding.EncodeToString(nonce)
	token := encodedNonce + "." + m.sign("login-csrf", encodedNonce)
	http.SetCookie(w, &http.Cookie{
		Name:     m.loginCSRFCookieName(),
		Value:    token,
		Path:     "/",
		MaxAge:   int(loginCSRFLifetime.Seconds()),
		Expires:  time.Now().Add(loginCSRFLifetime),
		Secure:   m.secure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	return token, nil
}

func (m *CookieManager) ValidateLoginCSRF(r *http.Request, submitted string) bool {
	cookie, err := r.Cookie(m.loginCSRFCookieName())
	if err != nil || !hmac.Equal([]byte(cookie.Value), []byte(submitted)) {
		return false
	}
	parts := strings.Split(submitted, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	return hmac.Equal([]byte(parts[1]), []byte(m.sign("login-csrf", parts[0])))
}

func (m *CookieManager) SetSession(w http.ResponseWriter, opaqueCredential string) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.sessionCookieName(),
		Value:    base64.RawURLEncoding.EncodeToString([]byte(opaqueCredential)),
		Path:     "/",
		Secure:   m.secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *CookieManager) Session(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(m.sessionCookieName())
	if err != nil || cookie.Value == "" {
		return "", false
	}
	credential, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(credential) == 0 {
		return "", false
	}
	return string(credential), true
}

func (m *CookieManager) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.sessionCookieName(),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
		Secure:   m.secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *CookieManager) SessionCSRF(opaqueCredential string) string {
	return m.sign("session-csrf", opaqueCredential)
}

func (m *CookieManager) ValidateSessionCSRF(opaqueCredential, submitted string) bool {
	if submitted == "" {
		return false
	}
	return hmac.Equal([]byte(submitted), []byte(m.SessionCSRF(opaqueCredential)))
}

func (m *CookieManager) sign(purpose, value string) string {
	mac := hmac.New(sha256.New, m.secret)
	_, _ = io.WriteString(mac, purpose)
	_, _ = io.WriteString(mac, "\x00")
	_, _ = io.WriteString(mac, value)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *CookieManager) sessionCookieName() string {
	if m.secure {
		return secureSessionCookie
	}
	return devSessionCookie
}

func (m *CookieManager) loginCSRFCookieName() string {
	if m.secure {
		return secureLoginCSRFCookie
	}
	return devLoginCSRFCookie
}
