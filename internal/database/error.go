package database

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/ncruces/go-sqlite3"
)

// ErrorDetails contains driver-neutral information suitable for a query error
// panel without discarding the original error chain.
type ErrorDetails struct {
	Message   string
	Code      string
	Line      int
	Column    int
	Position  int
	Duration  time.Duration
	Cancelled bool
	TimedOut  bool
}

// DescribeError extracts structured fields exposed by supported drivers.
func DescribeError(err error, duration time.Duration) ErrorDetails {
	details := ErrorDetails{Message: err.Error(), Duration: duration}
	if errors.Is(err, context.Canceled) {
		details.Message = "query cancelled"
		details.Cancelled = true
		return details
	}
	if errors.Is(err, context.DeadlineExceeded) {
		details.Message = "query timed out"
		details.TimedOut = true
		return details
	}

	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		details.Message = postgresError.Message
		details.Code = postgresError.Code
		details.Position = int(postgresError.Position)
		return details
	}

	var sqlServerError mssql.Error
	if errors.As(err, &sqlServerError) {
		details.Message = sqlServerError.Message
		details.Code = strconv.FormatInt(int64(sqlServerError.Number), 10)
		details.Line = int(sqlServerError.LineNo)
		return details
	}

	var sqliteError *sqlite3.Error
	if errors.As(err, &sqliteError) {
		details.Message = sqliteError.Error()
		details.Code = strconv.FormatInt(int64(sqliteError.ExtendedCode()), 10)
	}
	return details
}
