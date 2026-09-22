// Package demo serves a single-page operator console for showing the API
// working end to end.
//
// It is mounted only when DEMO_CONSOLE is true, and config.Validate refuses
// that setting in production. The page is unauthenticated — it has to be,
// since its first job is to log in — so it must not exist on a deployment
// holding real customer bookings. That is a configuration rule rather than
// a convention precisely because "we'll remember to turn it off" is not a
// control.
package demo

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed console.html
var consoleHTML []byte

// Register mounts the console. Taking the engine rather than a versioned
// group on purpose: this is a development surface, not part of the API
// contract, and it should not appear under /api/v1.
func Register(e *gin.Engine) {
	e.GET("/demo", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", consoleHTML)
	})
}
