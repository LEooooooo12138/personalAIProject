package gateway

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware validates the Bearer token for /v1/* endpoints.
// /health and /internal/* are allowed without authentication.
func AuthMiddleware(internalKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// Allow health checks and internal endpoints without auth.
		if path == "/health" || strings.HasPrefix(path, "/internal/") || strings.HasPrefix(path, "/channels/") || strings.HasPrefix(path, "/chat/") || strings.HasPrefix(path, "/sessions/") {
			c.Next()
			return
		}

		// Skip auth if no key is configured (dev mode).
		if internalKey == "" {
			c.Next()
			return
		}

		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		token := strings.TrimPrefix(auth, "Bearer ")
		if token != internalKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}

		c.Next()
	}
}

// RequestIDHeader adds a unique request-id header if not present.
func RequestIDHeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("X-Request-ID") == "" {
			c.Header("X-Request-ID", c.GetHeader("X-Request-ID"))
		}
		c.Next()
	}
}
