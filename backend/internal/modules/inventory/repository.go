package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

var ErrInvalidBatchQuery = errors.New("inventory: invalid batch query")

type Repository struct {
	db          *db.DB
	cursorCodec *pagination.CursorCodec
}

func NewRepository(database *db.DB, cursorCodec *pagination.CursorCodec) *Repository {
	return &Repository{db: database, cursorCodec: cursorCodec}
}

func (r *Repository) ListBatches(
	ctx context.Context,
	orgID uuid.UUID,
	filter batchListFilter,
	query *ListBatchesQuery,
) (BatchListResponse, error) {
	if r == nil || r.db == nil || r.cursorCodec == nil || query == nil || orgID == uuid.Nil {
		return BatchListResponse{}, ErrInvalidBatchQuery
	}
	if err := query.NormalizeAndValidate(); err != nil {
		return BatchListResponse{}, err
	}
	if len(filter.BranchScope) == 0 && filter.BranchScope != nil {
		return BatchListResponse{Items: []BatchListItem{}}, nil
	}

	sortKey := batchListSortKey(orgID, filter)
	var cursor *pagination.Cursor
	if query.Cursor != "" {
		decoded, err := r.cursorCodec.Decode(query.Cursor)
		if err != nil || decoded.ValidateSortKey(sortKey) != nil || decoded.CreatedAt.IsZero() {
			return BatchListResponse{}, ErrInvalidBatchQuery
		}
		cursor = &decoded
	}

	args := []any{orgID}
	where := []string{"b.organization_id = $1"}
	if filter.VariantID != nil {
		args = append(args, *filter.VariantID)
		where = append(where, "b.variant_id = $"+strconv.Itoa(len(args)))
	}
	if filter.Status != nil {
		args = append(args, string(*filter.Status))
		where = append(where, "b.status = $"+strconv.Itoa(len(args)))
	}
	if filter.BranchID != nil {
		args = append(args, *filter.BranchID)
		where = append(where, "bal.branch_id = $"+strconv.Itoa(len(args)))
	}
	if filter.WarehouseID != nil {
		args = append(args, *filter.WarehouseID)
		where = append(where, "bal.warehouse_id = $"+strconv.Itoa(len(args)))
	}
	havingSQL := ""
	if filter.Condition != nil {
		args = append(args, string(*filter.Condition))
		havingSQL = " HAVING bool_or(bal.condition = $" + strconv.Itoa(len(args)) + ")"
	}
	if filter.Query != "" {
		args = append(args, escapeLikePrefix(filter.Query)+"%")
		where = append(where, "b.normalized_lot_no LIKE $"+strconv.Itoa(len(args))+" ESCAPE E'\\\\'")
	}
	if len(filter.BranchScope) > 0 {
		args = append(args, filter.BranchScope)
		where = append(where, "bal.branch_id = ANY($"+strconv.Itoa(len(args))+"::uuid[])")
	}
	if cursor != nil {
		args = append(args, cursor.CreatedAt, cursor.ID)
		where = append(where, "(b.created_at, b.id) < ($"+strconv.Itoa(len(args)-1)+", $"+strconv.Itoa(len(args))+")")
	}

	args = append(args, query.Limit+1)
	limitPlaceholder := "$" + strconv.Itoa(len(args))
	const listSQL = `SELECT
		b.id, b.variant_id, b.display_lot_no, to_char(b.expires_on, 'YYYY-MM-DD'), b.expiry_precision,
		b.recall_status, b.status,
		COALESCE(sum(bal.on_hand_quantity) FILTER (WHERE bal.condition = 'sellable'), 0),
		COALESCE(sum(bal.reserved_quantity) FILTER (WHERE bal.condition = 'sellable'), 0),
		COALESCE(sum(bal.on_hand_quantity) FILTER (WHERE bal.condition <> 'sellable'), 0),
		b.created_at
	FROM inventory_batch_identities AS b
	LEFT JOIN inventory_balances AS bal
		ON bal.organization_id = b.organization_id AND bal.batch_id = b.id
	WHERE `
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx,
		listSQL+strings.Join(where, " AND ")+`
	GROUP BY b.id
	`+havingSQL+`
	ORDER BY b.created_at DESC, b.id DESC
	LIMIT `+limitPlaceholder,
		args...,
	)
	if err != nil {
		return BatchListResponse{}, err
	}
	defer rows.Close()

	items := make([]BatchListItem, 0, query.Limit)
	var lastCreatedAt time.Time
	response := BatchListResponse{Items: items}
	for rows.Next() {
		var item BatchListItem
		var createdAt time.Time
		if err := rows.Scan(
			&item.ID, &item.VariantID, &item.LotNumber, &item.ExpiryDate,
			&item.ExpiryPrecision, &item.RecallStatus, &item.Status,
			&item.SellableOnHand, &item.ActiveReserved, &item.NonSellableOnHand, &createdAt,
		); err != nil {
			return BatchListResponse{}, err
		}
		if len(items) == query.Limit {
			response.HasMore = true
			response.NextCursor = r.cursorCodec.Encode(pagination.Cursor{
				CreatedAt: lastCreatedAt,
				ID:        items[len(items)-1].ID,
				SortKey:   sortKey,
			})
			break
		}
		item.AvailableForSale = item.SellableOnHand - item.ActiveReserved
		items = append(items, item)
		lastCreatedAt = createdAt
	}
	if err := rows.Err(); err != nil {
		return BatchListResponse{}, err
	}
	response.Items = items
	return response, nil
}

func batchListSortKey(orgID uuid.UUID, filter batchListFilter) string {
	scope := make([]string, len(filter.BranchScope))
	for i, id := range filter.BranchScope {
		scope[i] = id.String()
	}
	sort.Strings(scope)
	payload := struct {
		OrgID       string
		BranchID    *uuid.UUID
		BranchScope []string
		WarehouseID *uuid.UUID
		VariantID   *uuid.UUID
		Status      *BatchStatus
		Condition   *StockCondition
		Query       string
	}{orgID.String(), filter.BranchID, scope, filter.WarehouseID, filter.VariantID, filter.Status, filter.Condition, filter.Query}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return "inventory-batches-v1:" + hex.EncodeToString(sum[:])
}

func escapeLikePrefix(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "%", `\%`)
	return strings.ReplaceAll(value, "_", `\_`)
}
