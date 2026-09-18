# Warehouse §84 branch-scope closure + rbac branch view — host verification.
# Run from repo root (D:\Codding\pharmaciano-backend-go\backend).
# The sandbox VM has no Go toolchain, so these must run on the host.

Write-Host "== 1. Build =="
go build ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== 2. Vet =="
go vet ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== 3. Warehouse module (branch-scope service edits; existing tests are pure helpers) =="
go test ./internal/modules/warehouse/...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== 4. RBAC enforcer (branch-aware tests incl. scoped resolve + branch token) =="
go test ./internal/modules/rbac/...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== 5. Auth/tenant chain (principal, branch scope, idle session) =="
go test ./internal/middleware/... ./internal/modules/auth/...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "Done."