package router

import (
	"github.com/gin-gonic/gin"
)

// docsDir is the on-disk location of the self-hosted API reference (Scalar).
// It resolves against the process working directory: under Docker the runtime
// WORKDIR is /app and the Dockerfile copies api/scalar there; in local dev the
// server runs from the repo root. Both hold api/scalar/{index.html,openapi.yaml}.
const docsDir = "api/scalar"

// registerDocs mounts the interactive OpenAPI reference and its bundled spec
// as PUBLIC endpoints (no token needed to VIEW docs — the underlying `/api/v1`
// module endpoints remain protected and require a bearer token).
//
//   GET /api-docs/            -> Scalar UI (index.html loads ./openapi.yaml)
//   GET /api-docs/openapi.yaml-> self-contained bundled spec (all paths inlined)
//   GET /api-docs            -> Gin's Static redirects to /api-docs/ for free
//
// For production behind a reverse proxy you may disable this by serving these
// two files from nginx instead and omitting the route.
func registerDocs(engine *gin.Engine, d Deps) {
	// Serve index.html + openapi.yaml (+ any future static assets) from disk.
	// Gin's Static honours http.Dir semantics: GET /api-docs/ serves ./index.html
	// and GET /api-docs auto-redirects to the trailing-slash form.
	engine.Static("/api-docs", docsDir)
}