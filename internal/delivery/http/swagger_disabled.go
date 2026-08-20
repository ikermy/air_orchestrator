//go:build prod

package web

import (
	"github.com/gin-gonic/gin"
)

func setupSwagger(r *gin.Engine) {
	// Swagger is disabled in production build
}
