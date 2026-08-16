// migrations/embed.go
package migrations

import "embed"

// FS embeds every migration file so cmd/migrate (and optionally cmd/api,
// if you ever want auto-migrate-at-boot) can read them without touching disk.
//
//go:embed *.sql
var FS embed.FS
