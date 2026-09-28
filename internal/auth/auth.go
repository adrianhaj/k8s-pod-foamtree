// Package auth puts an OIDC login in front of the dashboard. Sessions are
// stateless AES-GCM sealed cookies, so any replica can serve any request.
package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type Config struct {
	Issuer, ClientID, ClientSecret, RedirectURL string
	Scopes                                      []string
	GroupsClaim                                 string
	// Emails may use path.Match globs, e.g. "*@example.com".
	AllowedEmails, AllowedGroups []string
	// 32 bytes; a random key logs everyone out on restart.
	SessionKey []byte
}

type User struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

const (
	sessionCookie = "k8sfoams_session"
	loginCookie   = "k8sfoams_login"
	sessionTTL    = 8 * time.Hour
	loginTTL      = 10 * time.Minute
)

type Auth struct {
	cfg      Config
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	aead     cipher.AEAD
	secure   bool
}

func New(ctx context.Context, cfg Config) (*Auth, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.RedirectURL == "" {
		return nil, errors.New("oidc: issuer, client id and redirect url are required")
	}
	// An IdP like Google lets anyone sign in; an empty allowlist would too.
	if len(cfg.AllowedEmails) == 0 && len(cfg.AllowedGroups) == 0 {
		return nil, errors.New("oidc: set allowed emails or groups")
	}
	block, err := aes.NewCipher(cfg.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("session key: %w", err)
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	return &Auth{
		cfg: cfg,
		oauth: oauth2.Config{
			ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURL: cfg.RedirectURL,
			Endpoint: provider.Endpoint(), Scopes: cfg.Scopes,
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		aead:     aead,
		secure:   strings.HasPrefix(cfg.RedirectURL, "https://"),
	}, nil
}

func (a *Auth) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", a.login)
	mux.HandleFunc("GET /auth/callback", a.callback)
	mux.HandleFunc("POST /auth/logout", a.logout)
}

type userKey struct{}

func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey{}).(User)
	return u, ok
}

// Require lets a valid session through. Page loads are sent to the login;
// API calls get 401 so the UI can decide what to do.
func (a *Auth) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var u User
		if c, err := r.Cookie(sessionCookie); err == nil && a.open(sessionCookie, c.Value, &u) == nil {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, "/auth/login", http.StatusFound)
			return
		}
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
	})
}

type loginState struct {
	State, Nonce, Verifier string
}

func (a *Auth) login(w http.ResponseWriter, r *http.Request) {
	st := loginState{State: rand.Text(), Nonce: rand.Text(), Verifier: oauth2.GenerateVerifier()}
	a.setCookie(w, loginCookie, "/auth/", a.seal(loginCookie, st, loginTTL), int(loginTTL.Seconds()))
	http.Redirect(w, r, a.oauth.AuthCodeURL(st.State, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier)), http.StatusFound)
}

func (a *Auth) callback(w http.ResponseWriter, r *http.Request) {
	var st loginState
	c, err := r.Cookie(loginCookie)
	if err != nil || a.open(loginCookie, c.Value, &st) != nil {
		http.Error(w, "login expired, start again at /auth/login", http.StatusBadRequest)
		return
	}
	a.setCookie(w, loginCookie, "/auth/", "", -1)
	if subtle.ConstantTimeCompare([]byte(r.FormValue("state")), []byte(st.State)) != 1 {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	if e := r.FormValue("error"); e != "" {
		http.Error(w, "identity provider: "+e, http.StatusUnauthorized)
		return
	}
	tok, err := a.oauth.Exchange(r.Context(), r.FormValue("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		http.Error(w, "code exchange failed", http.StatusUnauthorized)
		slog.Warn("oidc exchange", "err", err)
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := a.verifier.Verify(r.Context(), raw)
	if err != nil || subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(st.Nonce)) != 1 {
		http.Error(w, "invalid id token", http.StatusUnauthorized)
		slog.Warn("oidc verify", "err", err)
		return
	}
	u, groups, err := a.claims(idt)
	if err != nil {
		http.Error(w, "unreadable id token claims", http.StatusUnauthorized)
		return
	}
	if !a.allowed(u.Email, groups) {
		slog.Info("login denied", "email", u.Email)
		http.Error(w, u.Email+" is not allowed to use this dashboard", http.StatusForbidden)
		return
	}
	a.setCookie(w, sessionCookie, "/", a.seal(sessionCookie, u, sessionTTL), int(sessionTTL.Seconds()))
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	a.setCookie(w, sessionCookie, "/", "", -1)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *Auth) claims(idt *oidc.IDToken) (User, []string, error) {
	var all map[string]json.RawMessage
	if err := idt.Claims(&all); err != nil {
		return User{}, nil, err
	}
	var c struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idt.Claims(&c); err != nil {
		return User{}, nil, err
	}
	// An unverified address is whatever the user typed: never match on it.
	if c.EmailVerified != nil && !*c.EmailVerified {
		c.Email = ""
	}
	var groups []string
	if g, ok := all[a.cfg.GroupsClaim]; ok {
		_ = json.Unmarshal(g, &groups) // a non-list claim simply grants nothing
	}
	return User{Email: c.Email, Name: c.Name}, groups, nil
}

func (a *Auth) allowed(email string, groups []string) bool {
	for _, pattern := range a.cfg.AllowedEmails {
		if ok, _ := path.Match(strings.ToLower(pattern), strings.ToLower(email)); ok && email != "" {
			return true
		}
	}
	return slices.ContainsFunc(groups, func(g string) bool { return slices.Contains(a.cfg.AllowedGroups, g) })
}

type sealed[T any] struct {
	V   T     `json:"v"`
	Exp int64 `json:"exp"`
}

// The cookie name is the AEAD's additional data, so a login cookie can never
// be replayed as a session.
func (a *Auth) seal(name string, v any, ttl time.Duration) string {
	b, _ := json.Marshal(sealed[any]{V: v, Exp: time.Now().Add(ttl).Unix()})
	return base64.RawURLEncoding.EncodeToString(a.aead.Seal(nil, nil, b, []byte(name)))
}

func (a *Auth) open(name, value string, v any) error {
	ct, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return err
	}
	b, err := a.aead.Open(nil, nil, ct, []byte(name))
	if err != nil {
		return err
	}
	s := sealed[json.RawMessage]{}
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if time.Now().Unix() > s.Exp {
		return errors.New("expired")
	}
	return json.Unmarshal(s.V, v)
}

// maxAge -1 deletes the cookie; 0 would silently turn it into a browser-session cookie.
func (a *Auth) setCookie(w http.ResponseWriter, name, path, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: path, MaxAge: maxAge,
		HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode,
	})
}
