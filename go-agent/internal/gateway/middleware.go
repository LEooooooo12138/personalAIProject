package gateway

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware defaults to denial. Browser cookies never authorize management.
func AuthMiddleware(internalKey string) gin.HandlerFunc {
	return newAccessControl(internalKey).middleware()
}

// RequestIDHeader adds a unique request-id header if not present.
func RequestIDHeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			b := make([]byte, 16)
			if _, err := rand.Read(b); err == nil {
				id = hex.EncodeToString(b)
			}
		}
		c.Header("X-Request-ID", id)
		c.Next()
	}
}
