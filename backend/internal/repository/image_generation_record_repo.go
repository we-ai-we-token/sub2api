package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type imageGenerationRecordRepository struct {
	db *sql.DB
}

func NewImageGenerationRecordRepository(db *sql.DB) service.ImageGenerationRecordRepository {
	return &imageGenerationRecordRepository{db: db}
}

const insertImageGenerationRecordSQL = `
INSERT INTO image_generation_records (
  request_id,
  client_request_id,
  user_id,
  api_key_id,
  account_id,
  group_id,
  platform,
  endpoint,
  model,
  upstream_model,
  stream,
  auth_ms,
  routing_ms,
  image_slot_wait_ms,
  user_slot_wait_ms,
  account_slot_wait_ms,
  upstream_ms,
  response_ms,
  first_token_ms,
  total_ms,
  attempts,
  account_switches,
  same_account_retries,
  attempts_detail,
  upstream_status_code,
  upstream_request_id,
  upstream_error_message,
  image_count,
  image_size,
  image_quality,
  success,
  status_code,
  error_type,
  created_at
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34
)`

func (r *imageGenerationRecordRepository) Insert(ctx context.Context, input *service.ImageGenerationRecordInput) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("nil image generation record repository")
	}
	if input == nil {
		return fmt.Errorf("nil input")
	}
	_, err := r.db.ExecContext(ctx, insertImageGenerationRecordSQL,
		opsNullString(input.RequestID),
		opsNullString(input.ClientRequestID),
		opsNullInt64(input.UserID),
		opsNullInt64(input.APIKeyID),
		opsNullInt64(input.AccountID),
		opsNullInt64(input.GroupID),
		input.Platform,
		input.Endpoint,
		input.Model,
		opsNullString(input.UpstreamModel),
		input.Stream,
		opsNullInt64(input.AuthMs),
		opsNullInt64(input.RoutingMs),
		opsNullInt64(input.ImageSlotWaitMs),
		opsNullInt64(input.UserSlotWaitMs),
		opsNullInt64(input.AccountSlotWaitMs),
		opsNullInt64(input.UpstreamMs),
		opsNullInt64(input.ResponseMs),
		opsNullInt64(input.FirstTokenMs),
		input.TotalMs,
		input.Attempts,
		input.AccountSwitches,
		input.SameAccountRetries,
		opsNullString(input.AttemptsDetailJSON()),
		opsNullInt(input.UpstreamStatusCode),
		opsNullString(input.UpstreamRequestID),
		opsNullString(input.UpstreamErrorMessage),
		input.ImageCount,
		opsNullString(input.ImageSize),
		opsNullString(input.ImageQuality),
		input.Success,
		opsNullInt(&input.StatusCode),
		opsNullString(input.ErrorType),
		input.CreatedAt,
	)
	return err
}

const listImageGenerationRecordColumns = `
  id, request_id, client_request_id, user_id, api_key_id, account_id, group_id,
  platform, endpoint, model, upstream_model, stream,
  auth_ms, routing_ms, image_slot_wait_ms, user_slot_wait_ms, account_slot_wait_ms,
  upstream_ms, response_ms, first_token_ms, total_ms,
  attempts, account_switches, same_account_retries, attempts_detail,
  upstream_status_code, upstream_request_id, upstream_error_message,
  image_count, image_size, image_quality,
  success, status_code, error_type, created_at`

func (r *imageGenerationRecordRepository) List(ctx context.Context, filter service.ImageGenerationRecordListFilter) ([]*service.ImageGenerationRecord, int64, error) {
	if r == nil || r.db == nil {
		return nil, 0, fmt.Errorf("nil image generation record repository")
	}

	where := []string{"created_at >= $1", "created_at < $2"}
	args := []any{filter.StartTime, filter.EndTime}
	next := 3

	addCond := func(cond string, value any) {
		where = append(where, fmt.Sprintf(cond, next))
		args = append(args, value)
		next++
	}

	if filter.Platform != "" {
		addCond("platform = $%d", filter.Platform)
	}
	if filter.Model != "" {
		addCond("model ILIKE $%d", filter.Model+"%")
	}
	if filter.Success != nil {
		addCond("success = $%d", *filter.Success)
	}
	if filter.UserID != nil {
		addCond("user_id = $%d", *filter.UserID)
	}
	if filter.AccountID != nil {
		addCond("account_id = $%d", *filter.AccountID)
	}
	if filter.GroupID != nil {
		addCond("group_id = $%d", *filter.GroupID)
	}
	if filter.MinTotalMs != nil {
		addCond("total_ms >= $%d", *filter.MinTotalMs)
	}

	whereClause := strings.Join(where, " AND ")

	var total int64
	countQuery := "SELECT COUNT(*) FROM image_generation_records WHERE " + whereClause
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := fmt.Sprintf(
		"SELECT %s FROM image_generation_records WHERE %s ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d",
		listImageGenerationRecordColumns, whereClause, next, next+1,
	)
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]*service.ImageGenerationRecord, 0, filter.PageSize)
	for rows.Next() {
		rec, err := scanImageGenerationRecord(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func scanImageGenerationRecord(rows *sql.Rows) (*service.ImageGenerationRecord, error) {
	var (
		rec                  service.ImageGenerationRecord
		requestID            sql.NullString
		clientRequestID      sql.NullString
		userID               sql.NullInt64
		apiKeyID             sql.NullInt64
		accountID            sql.NullInt64
		groupID              sql.NullInt64
		upstreamModel        sql.NullString
		authMs               sql.NullInt64
		routingMs            sql.NullInt64
		imageSlotWaitMs      sql.NullInt64
		userSlotWaitMs       sql.NullInt64
		accountSlotWaitMs    sql.NullInt64
		upstreamMs           sql.NullInt64
		responseMs           sql.NullInt64
		firstTokenMs         sql.NullInt64
		attemptsDetail       []byte
		upstreamStatusCode   sql.NullInt64
		upstreamRequestID    sql.NullString
		upstreamErrorMessage sql.NullString
		imageSize            sql.NullString
		imageQuality         sql.NullString
		statusCode           sql.NullInt64
		errorType            sql.NullString
	)
	if err := rows.Scan(
		&rec.ID, &requestID, &clientRequestID, &userID, &apiKeyID, &accountID, &groupID,
		&rec.Platform, &rec.Endpoint, &rec.Model, &upstreamModel, &rec.Stream,
		&authMs, &routingMs, &imageSlotWaitMs, &userSlotWaitMs, &accountSlotWaitMs,
		&upstreamMs, &responseMs, &firstTokenMs, &rec.TotalMs,
		&rec.Attempts, &rec.AccountSwitches, &rec.SameAccountRetries, &attemptsDetail,
		&upstreamStatusCode, &upstreamRequestID, &upstreamErrorMessage,
		&rec.ImageCount, &imageSize, &imageQuality,
		&rec.Success, &statusCode, &errorType, &rec.CreatedAt,
	); err != nil {
		return nil, err
	}
	rec.RequestID = requestID.String
	rec.ClientRequestID = clientRequestID.String
	rec.UserID = nullInt64PtrFromSQL(userID)
	rec.APIKeyID = nullInt64PtrFromSQL(apiKeyID)
	rec.AccountID = nullInt64PtrFromSQL(accountID)
	rec.GroupID = nullInt64PtrFromSQL(groupID)
	rec.UpstreamModel = upstreamModel.String
	rec.AuthMs = nullInt64PtrFromSQL(authMs)
	rec.RoutingMs = nullInt64PtrFromSQL(routingMs)
	rec.ImageSlotWaitMs = nullInt64PtrFromSQL(imageSlotWaitMs)
	rec.UserSlotWaitMs = nullInt64PtrFromSQL(userSlotWaitMs)
	rec.AccountSlotWaitMs = nullInt64PtrFromSQL(accountSlotWaitMs)
	rec.UpstreamMs = nullInt64PtrFromSQL(upstreamMs)
	rec.ResponseMs = nullInt64PtrFromSQL(responseMs)
	rec.FirstTokenMs = nullInt64PtrFromSQL(firstTokenMs)
	if len(attemptsDetail) > 0 {
		rec.AttemptsDetail = append([]byte(nil), attemptsDetail...)
	}
	if upstreamStatusCode.Valid {
		code := int(upstreamStatusCode.Int64)
		rec.UpstreamStatusCode = &code
	}
	rec.UpstreamRequestID = upstreamRequestID.String
	rec.UpstreamErrorMessage = upstreamErrorMessage.String
	rec.ImageSize = imageSize.String
	rec.ImageQuality = imageQuality.String
	if statusCode.Valid {
		rec.StatusCode = int(statusCode.Int64)
	}
	rec.ErrorType = errorType.String
	return &rec, nil
}

func nullInt64PtrFromSQL(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	value := v.Int64
	return &value
}
