package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lucasfguimares/tui-db/internal/profile"
)

// Index is one index of a relation with its key columns in key order.
type Index struct {
	Name      string
	Columns   []string
	IsUnique  bool
	IsPrimary bool
}

// RelationDetails holds metadata that is more expensive than the column list:
// size estimates, indexes, and the view definition.
type RelationDetails struct {
	ApproximateRows    int64
	HasApproximateRows bool
	TotalBytes         int64
	HasTotalBytes      bool
	Indexes            []Index
	Definition         string
}

// RelationDetails reads size estimates, indexes, and the view definition for a
// relation. Size estimates are best effort: missing permissions only clear them.
func (i *Inspector) RelationDetails(
	ctx context.Context,
	connection profile.Connection,
	schema string,
	relation string,
) (RelationDetails, error) {
	db, err := i.manager.Connection(ctx, connection)
	if err != nil {
		return RelationDetails{}, err
	}
	switch connection.Driver {
	case profile.DriverPostgres:
		return readPostgresDetails(ctx, db, schema, relation)
	case profile.DriverSQLServer:
		return readSQLServerDetails(ctx, db, schema, relation)
	case profile.DriverSQLite:
		return readSQLiteDetails(ctx, db, schema, relation)
	default:
		return RelationDetails{}, fmt.Errorf("catalog: unsupported driver %q", connection.Driver)
	}
}

func readPostgresDetails(ctx context.Context, db *sql.DB, schema, relation string) (RelationDetails, error) {
	details := RelationDetails{Indexes: []Index{}}
	var rows int64
	var bytes sql.NullInt64
	var kind string
	var definition sql.NullString
	err := db.QueryRowContext(ctx, `SELECT c.reltuples::bigint, pg_total_relation_size(c.oid), c.relkind::text,
  CASE WHEN c.relkind IN ('v', 'm') THEN pg_get_viewdef(c.oid, true) END
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2`, schema, relation).Scan(&rows, &bytes, &kind, &definition)
	if err != nil {
		return details, fmt.Errorf("reading relation details: %w", err)
	}
	if rows >= 0 && kind != "v" {
		details.ApproximateRows, details.HasApproximateRows = rows, true
	}
	if bytes.Valid && kind != "v" {
		details.TotalBytes, details.HasTotalBytes = bytes.Int64, true
	}
	details.Definition = strings.TrimSpace(definition.String)

	indexRows, err := db.QueryContext(ctx, `SELECT i.relname, ix.indisunique, ix.indisprimary, COALESCE(a.attname, '(expression)')
FROM pg_index ix
JOIN pg_class t ON t.oid = ix.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
JOIN pg_class i ON i.oid = ix.indexrelid
CROSS JOIN LATERAL unnest(ix.indkey::int2[]) WITH ORDINALITY AS k(attnum, position)
LEFT JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
WHERE n.nspname = $1 AND t.relname = $2 AND k.position <= ix.indnkeyatts
ORDER BY ix.indisprimary DESC, i.relname, k.position`, schema, relation)
	if err != nil {
		return details, fmt.Errorf("listing indexes: %w", err)
	}
	defer indexRows.Close()
	details.Indexes, err = scanIndexColumns(indexRows)
	return details, err
}

func readSQLServerDetails(ctx context.Context, db *sql.DB, schema, relation string) (RelationDetails, error) {
	details := RelationDetails{Indexes: []Index{}}
	object := "[" + strings.ReplaceAll(schema, "]", "]]") + "].[" + strings.ReplaceAll(relation, "]", "]]") + "]"
	var rows, bytes sql.NullInt64
	// dm_db_partition_stats needs VIEW DATABASE STATE; without it only the estimates are skipped.
	if err := db.QueryRowContext(ctx, `SELECT SUM(CASE WHEN index_id IN (0, 1) THEN row_count ELSE 0 END),
  SUM(reserved_page_count) * 8192
FROM sys.dm_db_partition_stats WHERE object_id = OBJECT_ID(@p1)`, object).Scan(&rows, &bytes); err == nil {
		details.ApproximateRows, details.HasApproximateRows = rows.Int64, rows.Valid
		details.TotalBytes, details.HasTotalBytes = bytes.Int64, bytes.Valid
	}
	var definition sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT OBJECT_DEFINITION(OBJECT_ID(@p1))`, object).Scan(&definition); err != nil {
		return details, fmt.Errorf("reading view definition: %w", err)
	}
	details.Definition = strings.TrimSpace(definition.String)

	indexRows, err := db.QueryContext(ctx, `SELECT i.name, i.is_unique, i.is_primary_key, c.name
FROM sys.indexes i
JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
WHERE i.object_id = OBJECT_ID(@p1) AND i.type > 0 AND ic.is_included_column = 0
ORDER BY i.is_primary_key DESC, i.name, ic.key_ordinal`, object)
	if err != nil {
		return details, fmt.Errorf("listing indexes: %w", err)
	}
	defer indexRows.Close()
	details.Indexes, err = scanIndexColumns(indexRows)
	return details, err
}

func readSQLiteDetails(ctx context.Context, db *sql.DB, schema, relation string) (RelationDetails, error) {
	details := RelationDetails{Indexes: []Index{}}
	var kind string
	var definition sql.NullString
	err := db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT type, sql FROM %s.sqlite_master WHERE name = %s AND type IN ('table', 'view')`,
		quoteIdentifier(schema), quoteLiteral(relation),
	)).Scan(&kind, &definition)
	if err != nil {
		return details, fmt.Errorf("reading relation details: %w", err)
	}
	if kind == "view" {
		details.Definition = strings.TrimSpace(definition.String)
		return details, nil
	}

	list, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA %s.index_list(%s)`, quoteIdentifier(schema), quoteLiteral(relation)))
	if err != nil {
		return details, fmt.Errorf("listing indexes: %w", err)
	}
	defer list.Close()
	for list.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := list.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			return details, fmt.Errorf("scanning sqlite index: %w", err)
		}
		details.Indexes = append(details.Indexes, Index{Name: name, IsUnique: unique != 0, IsPrimary: origin == "pk"})
	}
	if err := list.Err(); err != nil {
		return details, fmt.Errorf("iterating sqlite indexes: %w", err)
	}
	for index := range details.Indexes {
		columns, err := readSQLiteIndexColumns(ctx, db, schema, details.Indexes[index].Name)
		if err != nil {
			return details, err
		}
		details.Indexes[index].Columns = columns
	}
	return details, nil
}

func readSQLiteIndexColumns(ctx context.Context, db *sql.DB, schema, index string) ([]string, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA %s.index_info(%s)`, quoteIdentifier(schema), quoteLiteral(index)))
	if err != nil {
		return nil, fmt.Errorf("listing index columns: %w", err)
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var sequence, columnID int
		var name sql.NullString
		if err := rows.Scan(&sequence, &columnID, &name); err != nil {
			return nil, fmt.Errorf("scanning sqlite index column: %w", err)
		}
		if name.Valid {
			columns = append(columns, name.String)
		} else {
			columns = append(columns, "(expression)")
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating sqlite index columns: %w", err)
	}
	return columns, nil
}

// scanIndexColumns groups (index, unique, primary, column) rows, which must be
// ordered by index and key position, into indexes.
func scanIndexColumns(rows *sql.Rows) ([]Index, error) {
	indexes := []Index{}
	for rows.Next() {
		var name, column string
		var unique, primary bool
		if err := rows.Scan(&name, &unique, &primary, &column); err != nil {
			return nil, fmt.Errorf("scanning index: %w", err)
		}
		if len(indexes) == 0 || indexes[len(indexes)-1].Name != name {
			indexes = append(indexes, Index{Name: name, IsUnique: unique, IsPrimary: primary})
		}
		last := &indexes[len(indexes)-1]
		last.Columns = append(last.Columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating indexes: %w", err)
	}
	return indexes, nil
}
