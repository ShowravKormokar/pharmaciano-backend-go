package routes

import (
	v1 "backend/internal/routes/v1"

	"github.com/gin-gonic/gin"
)

func Register(r *gin.Engine) {
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	api := r.Group("/api")
	{
		v1.Register(api)
	}
}
