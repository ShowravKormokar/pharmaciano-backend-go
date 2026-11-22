package notification

import (
	"context"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

type Repository struct {
	db          *db.DB
	cursorCodec *pagination.CursorCodec
}

func NewRepository(database *db.DB, cursorCodec *pagination.CursorCodec) *Repository {
	return &Repository{db: database, cursorCodec: cursorCodec}
}

func (r *Repository) Ping(ctx context.Context) error {
	return nil
}
