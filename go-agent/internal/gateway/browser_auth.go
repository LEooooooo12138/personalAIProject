package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const browserCookieName = "agent_browser"

type principalKey struct{}
type principal struct {
	Owner string
	Admin bool
}
type accessControl struct {
	managementKey string
	signingKey    []byte
}

func newAccessControl(key string) *accessControl {
	secret := make([]byte, 32)
	if key != "" {
		sum := sha256.Sum256([]byte("agent-browser-cookie:" + key))
		copy(secret, sum[:])
	} else if _, err := rand.Read(secret); err != nil {
		return &accessControl{}
	}
	return &accessControl{managementKey: key, signingKey: secret}
}
func (a *accessControl) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/health" || path == "/chat" || strings.HasPrefix(path, "/chat/") || path == "/auth/browser" {
			c.Next()
			return
		}
		var p principal
		auth := c.GetHeader("Authorization")
		if a.managementKey != "" && strings.HasPrefix(auth, "Bearer ") && subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(a.managementKey)) == 1 {
			p = principal{Owner: "administrator", Admin: true}
		}
		browserRoute := path == "/channels/webchat/ws" || path == "/sessions" || strings.HasPrefix(path, "/sessions/")
		if !p.Admin && browserRoute {
			if !sameOrigin(c.Request) {
				c.AbortWithStatusJSON(403, gin.H{"error": "origin not allowed"})
				return
			}
			if cookie, err := c.Request.Cookie(browserCookieName); err == nil {
				p.Owner = a.verifyCookie(cookie.Value)
			}
		}
		if !p.Admin && (!browserRoute || p.Owner == "") {
			c.AbortWithStatusJSON(401, gin.H{"error": "authentication required"})
			return
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), principalKey{}, p))
		c.Next()
	}
}
func requestPrincipal(r *http.Request) principal {
	p, _ := r.Context().Value(principalKey{}).(principal)
	return p
}
func sameOrigin(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host)
}
func (a *accessControl) signature(payload string) string {
	mac := hmac.New(sha256.New, a.signingKey)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (a *accessControl) verifyCookie(value string) string {
	if len(a.signingKey) == 0 {
		return ""
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return ""
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(a.signature(payload)), []byte(parts[2])) {
		return ""
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() >= expires {
		return ""
	}
	if len(parts[0]) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		return ""
	}
	return parts[0]
}
func (a *accessControl) bootstrap(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.JSON(403, gin.H{"error": "origin not allowed"})
		return
	}
	c.Header("Cache-Control", "no-store")
	if len(a.signingKey) == 0 {
		c.JSON(503, gin.H{"error": "identity unavailable"})
		return
	}
	if old, err := c.Request.Cookie(browserCookieName); err == nil && a.verifyCookie(old.Value) != "" {
		c.JSON(200, gin.H{"status": "ok"})
		return
	}
	entropy := make([]byte, 32)
	if _, err := rand.Read(entropy); err != nil {
		c.JSON(503, gin.H{"error": "identity unavailable"})
		return
	}
	expires := time.Now().Add(30 * 24 * time.Hour)
	payload := hex.EncodeToString(entropy) + "." + strconv.FormatInt(expires.Unix(), 10)
	http.SetCookie(c.Writer, &http.Cookie{Name: browserCookieName, Value: payload + "." + a.signature(payload), Path: "/", Expires: expires, MaxAge: 30 * 24 * 3600, HttpOnly: true, Secure: c.Request.TLS != nil || c.Request.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode})
	c.JSON(200, gin.H{"status": "ok"})
}
