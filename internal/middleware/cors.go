package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS allows the configured browser origins.
//
// The allowed list is explicit rather than "*" because these routes carry a
// bearer token: a wildcard origin plus credentials is how one site's page
// gets to act as a logged-in user of another. An empty list means no
// cross-origin browser access at all, which is the right default for a
// service behind the same host as its UI.
func CORS(allowed []string) gin.HandlerFunc {
	index := make(map[string]bool, len(allowed))
	for _, o := range allowed {
		if o = strings.TrimSpace(o); o != "" {
			index[o] = true
		}
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && index[origin] {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			// Vary matters once more than one origin is allowed: without it
			// a shared cache can serve the header it computed for one
			// origin to a request from another.
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Max-Age", "600")
		}

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
