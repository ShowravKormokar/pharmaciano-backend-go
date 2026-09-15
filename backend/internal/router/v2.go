package router

import "github.com/gin-gonic/gin"

// registerV2 currently exposes the v1-compatible contract at the v2 boundary.
// Breaking changes can be introduced here by supplying a separate module list
// to registerVersion without changing the v1 routes.
func registerV2(engine *gin.Engine, d Deps) {
	registerVersion(engine, d, "v2", d.Modules)
}
