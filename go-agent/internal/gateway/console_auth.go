package gateway

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/console"
)

const consoleCookieName = "agent_console"

type consolePrincipalKey struct{}

func requestConsolePrincipal(r *http.Request) console.Principal {
	p, _ := r.Context().Value(consolePrincipalKey{}).(console.Principal)
	return p
}
func consoleToken(r *http.Request) string {
	cookie, err := r.Cookie(consoleCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Server) consoleMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.consoleAvailable(c) {
			return
		}
		path := c.Request.URL.Path
		if c.Request.Method == http.MethodGet && path == consolePrefix+"/auth/status" {
			c.Next()
			return
		}
		if c.Request.Method == http.MethodPost && path == consolePrefix+"/auth/login" {
			if !s.consoleOriginAllowed(c) {
				return
			}
			c.Next()
			return
		}
		token := consoleToken(c.Request)
		if token == "" {
			consoleError(c, 401, "unauthenticated", "Authentication required")
			return
		}
		p, err := s.consoleStore.Resolve(token)
		if err != nil {
			consoleStoreError(c, err)
			return
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), consolePrincipalKey{}, p))
		allowedRestricted := (path == consolePrefix+"/auth/me" && c.Request.Method == http.MethodGet) || (c.Request.Method == http.MethodPost && (path == consolePrefix+"/auth/password" || path == consolePrefix+"/auth/logout"))
		if p.MustChangePassword && !allowedRestricted {
			consoleError(c, 403, "password_change_required", "Change your temporary password first")
			return
		}
		if (path == consolePrefix+"/admin" || strings.HasPrefix(path, consolePrefix+"/admin/")) && p.Role != "admin" {
			consoleError(c, 403, "forbidden", "Permission denied")
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && c.Request.Method != http.MethodOptions {
			if !s.consoleOriginAllowed(c) {
				return
			}
			expected, err := s.consoleStore.CSRFToken(token)
			if err != nil {
				consoleStoreError(c, err)
				return
			}
			if subtle.ConstantTimeCompare([]byte(c.GetHeader("X-CSRF-Token")), []byte(expected)) != 1 {
				consoleError(c, 403, "csrf_failed", "Request verification failed")
				return
			}
		}
		c.Next()
	}
}

func (s *Server) consoleOriginAllowed(c *gin.Context) bool {
	if c.GetHeader("Origin") != s.cfg.Console.PublicOrigin {
		consoleError(c, 403, "origin_denied", "Origin is not allowed")
		return false
	}
	return true
}

func (s *Server) setConsoleCookie(c *gin.Context, token string, expires time.Time) {
	cookie := &http.Cookie{Name: consoleCookieName, Value: token, Path: consolePrefix, HttpOnly: true, Secure: strings.HasPrefix(s.cfg.Console.PublicOrigin, "https://"), SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: 7 * 24 * 3600}
	if token == "" {
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(1, 0)
	}
	http.SetCookie(c.Writer, cookie)
}

type consolePublicUser struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	DisplayName        string `json:"display_name"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"must_change_password"`
}

func publicConsoleUser(u console.User) consolePublicUser {
	return consolePublicUser{u.ID, u.Username, u.DisplayName, u.Role, u.MustChangePassword}
}

func (s *Server) consoleIdentity(c *gin.Context, token string, p console.Principal) bool {
	u, err := s.consoleStore.GetUser(p.UserID)
	if err != nil {
		consoleStoreError(c, err)
		return false
	}
	csrf, err := s.consoleStore.CSRFToken(token)
	if err != nil {
		consoleStoreError(c, err)
		return false
	}
	capabilities := s.consoleCapabilities(p.Role, p.MustChangePassword)
	c.JSON(200, gin.H{"user": publicConsoleUser(u), "capabilities": capabilities, "csrf_token": csrf})
	return true
}
func (s *Server) handleConsoleStatus(c *gin.Context) {
	c.JSON(200, gin.H{"initialized": s.consoleStore.Initialized()})
}
func (s *Server) handleConsoleMe(c *gin.Context) {
	s.consoleIdentity(c, consoleToken(c.Request), requestConsolePrincipal(c.Request))
}
func (s *Server) handleConsoleLogin(c *gin.Context) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeConsoleJSON(c, &input) {
		return
	}
	peer, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		peer = c.Request.RemoteAddr
	}
	if ip := net.ParseIP(peer); ip != nil {
		peer = ip.String()
	} else {
		peer = "unknown"
	}
	if !s.consoleLoginLimiter.allow(peer, input.Username) {
		c.Header("Retry-After", "300")
		consoleError(c, 429, "rate_limited", "Try again later")
		return
	}
	login, err := s.consoleStore.Authenticate(input.Username, input.Password)
	if err != nil {
		consoleStoreError(c, err)
		return
	}
	// Resolve identity before issuing a cookie; a concurrently revoked login fails closed.
	u, err := s.consoleStore.GetUser(login.Principal.UserID)
	if err != nil {
		consoleStoreError(c, err)
		return
	}
	csrf, err := s.consoleStore.CSRFToken(login.Token)
	if err != nil {
		consoleStoreError(c, err)
		return
	}
	capabilities := s.consoleCapabilities(u.Role, u.MustChangePassword)
	s.setConsoleCookie(c, login.Token, login.ExpiresAt)
	c.JSON(200, gin.H{"user": publicConsoleUser(u), "capabilities": capabilities, "csrf_token": csrf})
}
func (s *Server) handleConsoleLogout(c *gin.Context) {
	if err := s.consoleStore.Logout(consoleToken(c.Request)); err != nil {
		consoleStoreError(c, err)
		return
	}
	s.setConsoleCookie(c, "", time.Time{})
	c.Status(204)
}
func (s *Server) handleConsolePassword(c *gin.Context) {
	var input struct {
		Old string `json:"old_password"`
		New string `json:"new_password"`
	}
	if !decodeConsoleJSON(c, &input) {
		return
	}
	if err := s.consoleStore.ChangePassword(consoleToken(c.Request), input.Old, input.New); err != nil {
		consoleStoreError(c, err)
		return
	}
	s.setConsoleCookie(c, "", time.Time{})
	c.Status(204)
}
func (s *Server) handleConsoleBootstrap(c *gin.Context) {
	if !s.consoleAvailable(c) {
		return
	}
	var input struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if !decodeConsoleJSON(c, &input) {
		return
	}
	u, err := s.consoleStore.Bootstrap(input.Username, input.DisplayName, input.Password)
	if err != nil {
		consoleStoreError(c, err)
		return
	}
	c.JSON(201, gin.H{"user": publicConsoleUser(u)})
}
func (s *Server) handleConsoleResetAdmin(c *gin.Context) {
	if !s.consoleAvailable(c) {
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !decodeConsoleJSON(c, &input) {
		return
	}
	if err := s.consoleStore.ResetAdminPassword(input.Password); err != nil {
		consoleStoreError(c, err)
		return
	}
	c.Status(204)
}

type consoleAttemptBucket []time.Time
type consoleLoginLimiter struct {
	mu    sync.Mutex
	now   func() time.Time
	peers map[string]consoleAttemptBucket
	users map[string]consoleAttemptBucket
}

func newConsoleLoginLimiter(now func() time.Time) *consoleLoginLimiter {
	return &consoleLoginLimiter{now: now, peers: map[string]consoleAttemptBucket{}, users: map[string]consoleAttemptBucket{}}
}
func (l *consoleLoginLimiter) allow(peer, username string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, buckets := range []map[string]consoleAttemptBucket{l.peers, l.users} {
		for key, bucket := range buckets {
			first := 0
			for first < len(bucket) && !now.Before(bucket[first].Add(5*time.Minute)) {
				first++
			}
			if first == len(bucket) {
				delete(buckets, key)
			} else if first > 0 {
				buckets[key] = bucket[first:]
			}
		}
	}
	// Invalid usernames share a single per-peer bucket and cannot cause large keys.
	name := strings.ToLower(username)
	valid := len(name) >= 3 && len(name) <= 32
	for _, c := range username {
		if c > 127 {
			valid = false
		}
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			valid = false
		}
	}
	if !valid {
		name = "<invalid>"
	}
	key := peer + "\x00" + name
	ipBucket, ipExists := l.peers[peer]
	userBucket, userExists := l.users[key]
	if (!ipExists && len(l.peers) >= 4096) || (!userExists && len(l.users) >= 16384) {
		return false
	}
	if len(ipBucket) >= 60 || len(userBucket) >= 10 {
		return false
	}
	l.peers[peer] = append(ipBucket, now)
	l.users[key] = append(userBucket, now)
	return true
}

// A slow HA/model lookup can outlive logout, password rotation or disablement.
// Recheck the request cookie and its original principal immediately before
// publishing household information; shared upstream caches remain reusable.
func (s *Server) consoleReadJSON(c *gin.Context, data interface{}) {
	if err := c.Request.Context().Err(); err != nil {
		consoleStoreError(c, err)
		return
	}
	principal, err := s.consoleStore.Resolve(consoleToken(c.Request))
	if err != nil {
		consoleStoreError(c, err)
		return
	}
	if principal.MustChangePassword {
		consoleStoreError(c, console.ErrForbidden)
		return
	}
	if principal.UserID != requestConsolePrincipal(c.Request).UserID {
		consoleStoreError(c, console.ErrUnauthenticated)
		return
	}
	c.JSON(http.StatusOK, data)
}
