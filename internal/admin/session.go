package admin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

const sessionCookie = "curator_session"

// Session is the small signed state stored in the admin cookie.
type Session struct {
	Auth bool   `json:"a"`
	CSRF string `json:"c"`
}

type sessionKeyType struct{}

var sessionKey sessionKeyType

// loadAuth reads the admin password hash and ensures a session-signing secret
// exists. Auth is enabled only when a password has been set.
func (s *Server) loadAuth(ctx context.Context) error {
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return err
	}
	s.passwordHash = settings["admin.password_hash"]
	s.authEnabled = s.passwordHash != ""

	secret := settings["admin.session_secret"]
	if secret == "" {
		secret = randomToken()
		if err := s.store.SetSetting(ctx, "admin.session_secret", secret); err != nil {
			return err
		}
	}
	s.secret = []byte(secret)
	return nil
}

// withAuth enforces login and CSRF protection when auth is enabled.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authEnabled {
			next.ServeHTTP(w, r)
			return
		}

		sess, fresh := s.loadSession(r)
		if fresh {
			s.setSession(w, r, sess)
		}

		if r.Method == http.MethodPost {
			if token := r.FormValue("_csrf"); token == "" || !hmac.Equal([]byte(token), []byte(sess.CSRF)) {
				http.Error(w, "invalid CSRF token", http.StatusForbidden)
				return
			}
		}

		if !sess.Auth && r.URL.Path != s.path("/login") {
			s.redirect(w, r, s.link("login"), "")
			return
		}

		ctx := context.WithValue(r.Context(), sessionKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func sessionFrom(r *http.Request) Session {
	if sess, ok := r.Context().Value(sessionKey).(Session); ok {
		return sess
	}
	return Session{}
}

// loadSession returns the request's session, or a fresh one (with a new CSRF
// token) when the cookie is missing or invalid.
func (s *Server) loadSession(r *http.Request) (Session, bool) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if sess, ok := s.verify(c.Value); ok {
			return sess, false
		}
	}
	return Session{CSRF: randomToken()}, true
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, sess Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.sign(sess),
		Path:     s.cookiePath(),
		HttpOnly: true,
		Secure:   s.isSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     s.cookiePath(),
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func (s *Server) cookiePath() string {
	if s.basePath == "" {
		return "/"
	}
	return s.basePath + "/"
}

func (s *Server) isSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.trustProxy && r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Server) sign(sess Session) string {
	payload, _ := json.Marshal(sess)
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + s.mac(body)
}

func (s *Server) verify(value string) (Session, bool) {
	body, sig, ok := strings.Cut(value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.mac(body))) {
		return Session{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return Session{}, false
	}
	var sess Session
	if err := json.Unmarshal(payload, &sess); err != nil {
		return Session{}, false
	}
	return sess, true
}

func (s *Server) mac(body string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
