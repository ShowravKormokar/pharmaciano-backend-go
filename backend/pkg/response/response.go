package response

import "github.com/gin-gonic/gin"

type APIResponse struct {
	Success         bool        `json:"success"`
	IsAuthenticated bool        `json:"isAuthenticated,omitempty"`
	Message         string      `json:"message,omitempty"`
	Data            interface{} `json:"data,omitempty"`
	Error           interface{} `json:"error,omitempty"`
}

func Success(c *gin.Context, status int, message string, data interface{}) {
	c.JSON(status, APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

func SuccessAuth(c *gin.Context, status int, message string, data interface{}) {
	c.JSON(status, APIResponse{
		Success:         true,
		IsAuthenticated: true,
		Message:         message,
		Data:            data,
	})
}

func Error(c *gin.Context, status int, message string, err interface{}) {
	c.AbortWithStatusJSON(status, APIResponse{
		Success: false,
		Message: message,
		Error:   err,
	})
}
