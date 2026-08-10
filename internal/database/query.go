package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/lucasfguimares/tui-db/internal/profile"
	"github.com/lucasfguimares/tui-db/internal/sqleditor"
	"github.com/lucasfguimares/tui-db/internal/sqlscan"
	"github.com/ncruces/go-sqlite3/driver"
)

// ResultColumn describes one returned result column and its proven database
// origin, when the driver can provide one.
type ResultColumn struct {
	Name            string
	DatabaseType    string
	Nullable        bool
	NullableKnown   bool
	Length          int64
	HasLength       bool
	Precision       int64
	Scale           int64
	HasDecimalSize  bool
	Ordinal         int
	Key             string
	SourceDatabase  string
	SourceSchema    string
	SourceTable     string
	SourceColumn    string
	ReferenceSchema string
	ReferenceTable  string
	ReferenceColumn string

	sourceObjectID uint32
	sourceColumnID int
}

// Result contains either typed tabular rows or command metadata. Cell values
// remain unformatted so the UI can render them without destroying the original.
type Result struct {
	Columns          []ResultColumn
	Rows             [][]any
	AffectedRows     int64
	Duration         time.Duration
	QueryDuration    time.Duration
	FetchDuration    time.Duration
	MetadataDuration time.Duration
	Limit            int
	IsTruncated      bool
}

// Runner executes one bounded SQL statement at a time.
type Runner struct {
	manager *Manager
}

// NewRunner creates a query runner.
func NewRunner(manager *Manager) *Runner {
	return &Runner{manager: manager}
}

// Run executes exactly one statement with the profile timeout and row limit.
func (r *Runner) Run(
	ctx context.Context,
	connection profile.Connection,
	statement string,
) (Result, error) {
	spans := sqlscan.Statements(statement)
	if len(spans) != 1 {
		return Result{}, errors.New("query: select exactly one sql statement")
	}
	return r.run(ctx, connection, strings.TrimSpace(spans[0].Text))
}

// RunScript executes the complete non-empty script. It is intentionally a
// separate entry point so callers cannot accidentally bypass Run's one-statement
// safety boundary. Drivers return the first available rowset for batch queries.
func (r *Runner) RunScript(ctx context.Context, connection profile.Connection, script string) (Result, error) {
	if len(sqlscan.Statements(script)) == 0 {
		return Result{}, errors.New("query: script is empty")
	}
	return r.run(ctx, connection, strings.TrimSpace(script))
}

func (r *Runner) run(ctx context.Context, connection profile.Connection, statement string) (Result, error) {
	statement = statementForConnection(connection, statement)
	analysis := sqlscan.Analyze(statement)
	db, err := r.manager.Connection(ctx, connection)
	if err != nil {
		return Result{}, err
	}

	queryCtx, cancel := context.WithTimeout(
		ctx,
		time.Duration(connection.QueryTimeoutSeconds)*time.Second,
	)
	defer cancel()
	started := time.Now()
	if analysis.ReturnsRows {
		result, queryErr := runQuery(
			queryCtx,
			db,
			connection,
			statement,
			connection.RowLimit,
		)
		result.Duration = result.QueryDuration + result.FetchDuration
		if result.Duration == 0 {
			result.Duration = time.Since(started)
			if result.Duration == 0 {
				result.Duration = time.Nanosecond
			}
		}
		return result, queryErr
	}

	command, err := db.ExecContext(queryCtx, statement)
	queryDuration := time.Since(started)
	if err != nil {
		return Result{
			Duration:      queryDuration,
			QueryDuration: queryDuration,
			Limit:         connection.RowLimit,
		}, fmt.Errorf("executing statement: %w", withContextError(queryCtx, err))
	}
	affected, err := command.RowsAffected()
	if err != nil {
		affected = -1
	}
	return Result{
		AffectedRows:  affected,
		Duration:      time.Since(started),
		QueryDuration: queryDuration,
		Limit:         connection.RowLimit,
	}, nil
}

func statementForConnection(connection profile.Connection, statement string) string {
	if connection.DefaultSchema == "" {
		return statement
	}
	switch connection.Driver {
	case profile.DriverSQLServer:
		return sqleditor.QualifyRelations(statement, sqleditor.TSQL(), connection.DefaultSchema)
	case profile.DriverSQLite:
		return sqleditor.QualifyRelations(statement, sqleditor.SQLite(), connection.DefaultSchema)
	default:
		return statement
	}
}

func runQuery(
	ctx context.Context,
	db *sql.DB,
	connection profile.Connection,
	statement string,
	limit int,
) (Result, error) {
	queryStarted := time.Now()
	rows, err := db.QueryContext(ctx, statement)
	queryDuration := time.Since(queryStarted)
	if err != nil {
		return Result{
			QueryDuration: queryDuration,
			Limit:         limit,
		}, fmt.Errorf("executing query: %w", withContextError(ctx, err))
	}

	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		_ = rows.Close()
		return Result{
			QueryDuration: queryDuration,
			Limit:         limit,
		}, fmt.Errorf("reading result columns: %w", err)
	}
	columns := make([]ResultColumn, 0, len(columnTypes))
	for index, columnType := range columnTypes {
		nullable, nullableKnown := columnType.Nullable()
		length, hasLength := columnType.Length()
		precision, scale, hasDecimalSize := columnType.DecimalSize()
		columns = append(columns, ResultColumn{
			Name:           columnType.Name(),
			DatabaseType:   columnType.DatabaseTypeName(),
			Nullable:       nullable,
			NullableKnown:  nullableKnown,
			Length:         length,
			HasLength:      hasLength,
			Precision:      precision,
			Scale:          scale,
			HasDecimalSize: hasDecimalSize,
			Ordinal:        index + 1,
		})
	}

	result := Result{
		Columns:       columns,
		Rows:          make([][]any, 0, min(limit, 64)),
		QueryDuration: queryDuration,
		Limit:         limit,
	}
	fetchStarted := time.Now()
	for rows.Next() {
		if len(result.Rows) >= limit {
			result.IsTruncated = true
			break
		}
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			result.FetchDuration = time.Since(fetchStarted)
			_ = rows.Close()
			return result, fmt.Errorf("scanning result row: %w", withContextError(ctx, err))
		}
		for i := range values {
			values[i] = cloneDatabaseValue(values[i])
		}
		result.Rows = append(result.Rows, values)
	}
	result.FetchDuration = time.Since(fetchStarted)
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if iterationErr != nil {
		return result, fmt.Errorf("iterating result rows: %w", withContextError(ctx, iterationErr))
	}
	if closeErr != nil {
		return result, fmt.Errorf("closing result rows: %w", closeErr)
	}

	metadataStarted := time.Now()
	describeResultOrigins(ctx, db, connection, statement, result.Columns)
	enrichResultColumns(ctx, db, connection, result.Columns)
	result.MetadataDuration = time.Since(metadataStarted)
	return result, nil
}

func cloneDatabaseValue(value any) any {
	if bytes, ok := value.([]byte); ok {
		return append([]byte{}, bytes...)
	}
	return value
}

func withContextError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return errors.Join(err, contextErr)
	}
	return err
}

func describeResultOrigins(
	ctx context.Context,
	db *sql.DB,
	connection profile.Connection,
	statement string,
	columns []ResultColumn,
) {
	var err error
	switch connection.Driver {
	case profile.DriverPostgres:
		err = describePostgresOrigins(ctx, db, statement, columns)
	case profile.DriverSQLServer:
		err = describeSQLServerOrigins(ctx, db, statement, columns)
	case profile.DriverSQLite:
		err = describeSQLiteOrigins(ctx, db, statement, columns)
	}
	_ = err // Origin metadata is optional; query data remains valid without it.
}

func describePostgresOrigins(
	ctx context.Context,
	db *sql.DB,
	statement string,
	columns []ResultColumn,
) error {
	connection, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	return connection.Raw(func(driverConnection any) error {
		stdlibConnection, ok := driverConnection.(*stdlib.Conn)
		if !ok {
			return errors.New("postgres driver does not expose result metadata")
		}
		description, err := stdlibConnection.Conn().Prepare(ctx, "", statement)
		if err != nil {
			return err
		}
		for index, field := range description.Fields {
			if index >= len(columns) {
				break
			}
			columns[index].sourceObjectID = field.TableOID
			columns[index].sourceColumnID = int(field.TableAttributeNumber)
		}
		return nil
	})
}

func describeSQLiteOrigins(
	ctx context.Context,
	db *sql.DB,
	statement string,
	columns []ResultColumn,
) error {
	connection, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	return connection.Raw(func(driverConnection any) error {
		sqliteConnection, ok := driverConnection.(driver.Conn)
		if !ok {
			return errors.New("sqlite driver does not expose result metadata")
		}
		statementHandle, _, err := sqliteConnection.Raw().Prepare(statement)
		if err != nil {
			return err
		}
		defer statementHandle.Close()
		for index := range min(statementHandle.ColumnCount(), len(columns)) {
			columns[index].SourceSchema = statementHandle.ColumnDatabaseName(index)
			columns[index].SourceTable = statementHandle.ColumnTableName(index)
			columns[index].SourceColumn = statementHandle.ColumnOriginName(index)
		}
		return nil
	})
}

func describeSQLServerOrigins(
	ctx context.Context,
	db *sql.DB,
	statement string,
	columns []ResultColumn,
) error {
	const query = `SELECT
    column_ordinal,
		system_type_name,
    is_nullable,
    CONVERT(bigint, max_length),
    CONVERT(bigint, precision),
		CONVERT(bigint, scale),
		source_database,
		source_schema,
    source_table,
    source_column
FROM sys.dm_exec_describe_first_result_set(@p1, NULL, 1)
WHERE is_hidden = 0
ORDER BY column_ordinal`
	rows, err := db.QueryContext(ctx, query, statement)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ordinal int
		var databaseType sql.NullString
		var nullable sql.NullBool
		var length, precision, scale sql.NullInt64
		var databaseName, schema, table, column sql.NullString
		if err := rows.Scan(
			&ordinal,
			&databaseType,
			&nullable,
			&length,
			&precision,
			&scale,
			&databaseName,
			&schema,
			&table,
			&column,
		); err != nil {
			return err
		}
		if ordinal < 1 || ordinal > len(columns) {
			continue
		}
		resultColumn := &columns[ordinal-1]
		if databaseType.Valid {
			resultColumn.DatabaseType = databaseType.String
		}
		resultColumn.Nullable = nullable.Bool
		resultColumn.NullableKnown = nullable.Valid
		resultColumn.Length, resultColumn.HasLength = length.Int64, length.Valid
		resultColumn.Precision = precision.Int64
		resultColumn.Scale = scale.Int64
		resultColumn.HasDecimalSize = precision.Valid
		resultColumn.SourceDatabase = databaseName.String
		resultColumn.SourceSchema = schema.String
		resultColumn.SourceTable = table.String
		resultColumn.SourceColumn = column.String
	}
	return rows.Err()
}

func enrichResultColumns(
	ctx context.Context,
	db *sql.DB,
	connection profile.Connection,
	columns []ResultColumn,
) {
	cache := map[string][]Column{}
	postgresCache := map[uint32][]Column{}
	for index := range columns {
		resultColumn := &columns[index]
		if connection.Driver == profile.DriverPostgres && resultColumn.sourceObjectID != 0 {
			metadata, ok := postgresCache[resultColumn.sourceObjectID]
			if !ok {
				var err error
				metadata, err = readPostgresColumnsByID(ctx, db, resultColumn.sourceObjectID)
				if err != nil {
					continue
				}
				postgresCache[resultColumn.sourceObjectID] = metadata
			}
			for _, sourceColumn := range metadata {
				if sourceColumn.Ordinal == resultColumn.sourceColumnID {
					applyColumnMetadata(resultColumn, sourceColumn)
					break
				}
			}
			continue
		}
		if resultColumn.SourceTable == "" || resultColumn.SourceColumn == "" {
			continue
		}
		if connection.Driver == profile.DriverSQLServer && resultColumn.SourceDatabase != "" &&
			!strings.EqualFold(resultColumn.SourceDatabase, connection.Database) {
			continue
		}
		key := resultColumn.SourceSchema + "\x00" + resultColumn.SourceTable
		metadata, ok := cache[key]
		if !ok {
			var err error
			metadata, err = readColumns(
				ctx,
				db,
				connection,
				resultColumn.SourceSchema,
				resultColumn.SourceTable,
			)
			if err != nil {
				continue
			}
			cache[key] = metadata
		}
		for _, sourceColumn := range metadata {
			if sourceColumn.Name == resultColumn.SourceColumn {
				applyColumnMetadata(resultColumn, sourceColumn)
				break
			}
		}
	}
}

func readPostgresColumnsByID(
	ctx context.Context,
	db *sql.DB,
	objectID uint32,
) ([]Column, error) {
	const query = `SELECT ns.nspname, cls.relname
FROM pg_catalog.pg_class cls
JOIN pg_catalog.pg_namespace ns ON ns.oid = cls.relnamespace
WHERE cls.oid = $1`
	var schema, table string
	if err := db.QueryRowContext(ctx, query, objectID).Scan(&schema, &table); err != nil {
		return nil, err
	}
	columns, err := readPostgresColumns(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	for index := range columns {
		columns[index].Schema = schema
		columns[index].Table = table
	}
	return columns, nil
}

func applyColumnMetadata(resultColumn *ResultColumn, source Column) {
	resultColumn.DatabaseType = source.Type
	resultColumn.Nullable = source.Nullable
	resultColumn.NullableKnown = true
	resultColumn.Length = source.Length
	resultColumn.HasLength = source.HasLength
	resultColumn.Precision = source.Precision
	resultColumn.Scale = source.Scale
	resultColumn.HasDecimalSize = source.HasDecimalSize
	resultColumn.Ordinal = source.Ordinal
	resultColumn.Key = source.Key
	resultColumn.SourceSchema = firstNonEmpty(resultColumn.SourceSchema, source.Schema)
	resultColumn.SourceTable = firstNonEmpty(resultColumn.SourceTable, source.Table)
	resultColumn.SourceColumn = firstNonEmpty(resultColumn.SourceColumn, source.Name)
	resultColumn.ReferenceSchema = source.ReferenceSchema
	resultColumn.ReferenceTable = source.ReferenceTable
	resultColumn.ReferenceColumn = source.ReferenceColumn
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
