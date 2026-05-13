package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/shellhaki/openCBT/internal/auth"
	"github.com/shellhaki/openCBT/internal/httpx"
)

const (
	UserIDKey = "user_id"
	RoleKey   = "role"
)

func Auth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			httpx.Error(c, http.StatusUnauthorized, "missing bearer token")
			c.Abort()
			return
		}

		claims, err := auth.ParseToken(strings.TrimPrefix(header, "Bearer "), secret)
		if err != nil {
			httpx.Error(c, http.StatusUnauthorized, "invalid token")
			c.Abort()
			return
		}

		c.Set(UserIDKey, claims.UserID)
		c.Set(RoleKey, claims.Role)
		c.Next()
	}
}

func RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString(RoleKey) != role {
			httpx.Error(c, http.StatusForbidden, "this endpoint is not available for your role")
			c.Abort()
			return
		}
		c.Next()
	}
}
