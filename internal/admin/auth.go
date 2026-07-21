package admin

import (
	"log"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled {
		s.redirect(w, r, s.link(), "")
		return
	}
	if sessionFrom(r).Auth {
		s.redirect(w, r, s.link(), "")
		return
	}
	s.render(w, r, "login", "Sign in", s.flash(r), nil)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled {
		s.redirect(w, r, s.link(), "")
		return
	}

	password := r.FormValue("password")
	if bcrypt.CompareHashAndPassword([]byte(s.passwordHash), []byte(password)) != nil {
		ip := s.clientIP(r)
		delay := s.throttle.fail(ip)
		log.Printf("admin: failed login from %s", ip)
		if !s.applyLoginDelay(w, r, delay) {
			return
		}
		s.redirect(w, r, s.link("login"), "Invalid password")
		return
	}

	s.throttle.success(s.clientIP(r))
	s.setSession(w, r, Session{Auth: true, CSRF: randomToken()})
	s.redirect(w, r, s.link(), "Signed in")
}

// applyLoginDelay sleeps for the throttle delay, bounding the number of
// simultaneously delayed logins. It returns false (after responding) when the
// request should not proceed.
func (s *Server) applyLoginDelay(w http.ResponseWriter, r *http.Request, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	select {
	case s.loginSem <- struct{}{}:
		defer func() { <-s.loginSem }()
	default:
		http.Error(w, "too many login attempts, slow down", http.StatusTooManyRequests)
		return false
	}
	select {
	case <-time.After(delay):
		return true
	case <-r.Context().Done():
		return false
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w)
	s.redirect(w, r, s.link("login"), "Signed out")
}
