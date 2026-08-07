package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lucasfguimares/tui-db/internal/profile"
)

// Relation is a table or view in a database schema.
type Relation struct {
	Schema string
	Name   string
	Type   string
}

// Column describes a relation column for the schema browser.
type Column struct {
	Schema          string
	Table           string
	Name            string
	Type            string
	Nullable        bool
	Default         string
	Key             string
	Ordinal         int
	Length          int64
	HasLength       bool
	Precision       int64
	Scale           int64
	HasDecimalSize  bool
	ReferenceSchema string
	ReferenceTable  string
	ReferenceColumn string
}

// Inspector reads normalized schema metadata through engine-specific queries.
type Inspector struct {
	manager *Manager
}

// NewInspector creates a metadata inspector.
func NewInspector(manager *Manager) *Inspector {
	return &Inspector{manager: manager}
}

// Schemas lists user-visible schemas or attached SQLite databases.
func (i *Inspector) Schemas(ctx context.Context, connection profile.Connection) ([]string, error) {
	db, err := i.manager.Connection(ctx, connection)
	if err != nil {
		return nil, err
	}

	query := ""
	switch connection.Driver {
	case profile.DriverPostgres:
		query = `SELECT schema_name FROM information_schema.schemata
WHERE schema_name <> 'information_schema' AND schema_name NOT LIKE 'pg_%' ORDER BY schema_name`
	case profile.DriverSQLServer:
		query = `SELECT name FROM sys.schemas WHERE name NOT IN ('sys', 'INFORMATION_SCHEMA') ORDER BY name`
	case profile.DriverSQLite:
		query = `PRAGMA database_list`
	default:
		return nil, fmt.Errorf("catalog: unsupported driver %q", connection.Driver)
	}

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing schemas: %w", err)
	}
	defer rows.Close()

	schemas := []string{}
	for rows.Next() {
		var name string
		if connection.Driver == profile.DriverSQLite {
			var sequence int
			var path string
			if err := rows.Scan(&sequence, &name, &path); err != nil {
				return nil, fmt.Errorf("scanning sqlite database: %w", err)
			}
		} else if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scanning schema: %w", err)
		}
		schemas = append(schemas, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating schemas: %w", err)
	}
	return schemas, nil
}

// Relations lists tables and views in schema.
func (i *Inspector) Relations(
	ctx context.Context,
	connection profile.Connection,
	schema string,
) ([]Relation, error) {
	db, err := i.manager.Connection(ctx, connection)
	if err != nil {
		return nil, err
	}

	var rows *sql.Rows
	switch connection.Driver {
	case profile.DriverPostgres:
		rows, err = db.QueryContext(ctx, `SELECT table_schema, table_name, table_type
FROM information_schema.tables WHERE table_schema = $1 ORDER BY table_type, table_name`, schema)
	case profile.DriverSQLServer:
		rows, err = db.QueryContext(ctx, `SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE
FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = @p1 ORDER BY TABLE_TYPE, TABLE_NAME`, schema)
	case profile.DriverSQLite:
		query := fmt.Sprintf(
			`SELECT %s, name, type FROM %s.sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%%' ORDER BY type, name`,
			quoteLiteral(schema),
			quoteIdentifier(schema),
		)
		rows, err = db.QueryContext(ctx, query)
	default:
		return nil, fmt.Errorf("catalog: unsupported driver %q", connection.Driver)
	}
	if err != nil {
		return nil, fmt.Errorf("listing relations: %w", err)
	}
	defer rows.Close()

	relations := []Relation{}
	for rows.Next() {
		var relation Relation
		if err := rows.Scan(&relation.Schema, &relation.Name, &relation.Type); err != nil {
			return nil, fmt.Errorf("scanning relation: %w", err)
		}
		relations = append(relations, relation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating relations: %w", err)
	}
	return relations, nil
}

// Columns lists relation columns in ordinal order.
func (i *Inspector) Columns(
	ctx context.Context,
	connection profile.Connection,
	schema string,
	relation string,
) ([]Column, error) {
	db, err := i.manager.Connection(ctx, connection)
	if err != nil {
		return nil, err
	}
	return readColumns(ctx, db, connection, schema, relation)
}

func readColumns(
	ctx context.Context,
	db *sql.DB,
	connection profile.Connection,
	schema string,
	relation string,
) ([]Column, error) {
	if connection.Driver == profile.DriverSQLite {
		return readSQLiteColumns(ctx, db, schema, relation)
	}

	if connection.Driver == profile.DriverPostgres {
		return readPostgresColumns(ctx, db, schema, relation)
	}
	if connection.Driver == profile.DriverSQLServer {
		return readSQLServerColumns(ctx, db, schema, relation)
	}
	return nil, fmt.Errorf("catalog: unsupported driver %q", connection.Driver)
}

type foreignKeyTarget struct {
	schema string
	table  string
	column string
}

func readSQLiteColumns(
	ctx context.Context,
	db *sql.DB,
	schema string,
	relation string,
) ([]Column, error) {
	foreignKeys, err := readSQLiteForeignKeys(ctx, db, schema, relation)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(
		`PRAGMA %s.table_info(%s)`,
		quoteIdentifier(schema),
		quoteLiteral(relation),
	)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing columns: %w", err)
	}
	defer rows.Close()

	columns := []Column{}
	for rows.Next() {
		var ordinal, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&ordinal, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("scanning sqlite column: %w", err)
		}
		target, isForeignKey := foreignKeys[name]
		columns = append(columns, Column{
			Name:            name,
			Type:            dataType,
			Nullable:        notNull == 0 && primaryKey == 0,
			Default:         defaultValue.String,
			Key:             columnKey(primaryKey > 0, isForeignKey),
			Ordinal:         ordinal + 1,
			ReferenceSchema: target.schema,
			ReferenceTable:  target.table,
			ReferenceColumn: target.column,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating sqlite columns: %w", err)
	}
	return columns, nil
}

func readSQLiteForeignKeys(
	ctx context.Context,
	db *sql.DB,
	schema string,
	relation string,
) (map[string]foreignKeyTarget, error) {
	query := fmt.Sprintf(
		`PRAGMA %s.foreign_key_list(%s)`,
		quoteIdentifier(schema),
		quoteLiteral(relation),
	)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing sqlite foreign keys: %w", err)
	}
	defer rows.Close()

	targets := map[string]foreignKeyTarget{}
	for rows.Next() {
		var id, sequence int
		var table, from, onUpdate, onDelete, match string
		var to sql.NullString
		if err := rows.Scan(&id, &sequence, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return nil, fmt.Errorf("scanning sqlite foreign key: %w", err)
		}
		targets[from] = foreignKeyTarget{schema: schema, table: table, column: to.String}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating sqlite foreign keys: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("closing sqlite foreign keys: %w", err)
	}
	for source, target := range targets {
		if target.column != "" {
			continue
		}
		target.column, err = readSQLitePrimaryKeyColumn(ctx, db, target.schema, target.table)
		if err != nil {
			return nil, err
		}
		targets[source] = target
	}
	return targets, nil
}

func readSQLitePrimaryKeyColumn(
	ctx context.Context,
	db *sql.DB,
	schema string,
	relation string,
) (string, error) {
	query := fmt.Sprintf(
		`PRAGMA %s.table_info(%s)`,
		quoteIdentifier(schema),
		quoteLiteral(relation),
	)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return "", fmt.Errorf("listing referenced sqlite columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ordinal, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&ordinal, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return "", fmt.Errorf("scanning referenced sqlite column: %w", err)
		}
		if primaryKey == 1 {
			return name, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterating referenced sqlite columns: %w", err)
	}
	return "", nil
}

func readPostgresColumns(
	ctx context.Context,
	db *sql.DB,
	schema string,
	relation string,
) ([]Column, error) {
	const query = `SELECT
    a.attname,
    pg_catalog.format_type(a.atttypid, a.atttypmod),
    NOT a.attnotnull,
    COALESCE(pg_catalog.pg_get_expr(ad.adbin, ad.adrelid), ''),
    a.attnum,
    isc.character_maximum_length,
    isc.numeric_precision,
    isc.numeric_scale,
    EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint pk
        WHERE pk.conrelid = cls.oid AND pk.contype = 'p' AND a.attnum = ANY(pk.conkey)
    ),
    fk.schema_name,
    fk.table_name,
    fk.column_name
FROM pg_catalog.pg_class cls
JOIN pg_catalog.pg_namespace ns ON ns.oid = cls.relnamespace
JOIN pg_catalog.pg_attribute a ON a.attrelid = cls.oid AND a.attnum > 0 AND NOT a.attisdropped
LEFT JOIN pg_catalog.pg_attrdef ad ON ad.adrelid = cls.oid AND ad.adnum = a.attnum
LEFT JOIN information_schema.columns isc
    ON isc.table_schema = ns.nspname AND isc.table_name = cls.relname AND isc.column_name = a.attname
LEFT JOIN LATERAL (
    SELECT ref_ns.nspname AS schema_name, ref_cls.relname AS table_name, ref_a.attname AS column_name
    FROM pg_catalog.pg_constraint con
    JOIN LATERAL generate_subscripts(con.conkey, 1) pos ON TRUE
    JOIN pg_catalog.pg_class ref_cls ON ref_cls.oid = con.confrelid
    JOIN pg_catalog.pg_namespace ref_ns ON ref_ns.oid = ref_cls.relnamespace
    JOIN pg_catalog.pg_attribute ref_a
        ON ref_a.attrelid = ref_cls.oid AND ref_a.attnum = con.confkey[pos]
    WHERE con.conrelid = cls.oid AND con.contype = 'f' AND con.conkey[pos] = a.attnum
    LIMIT 1
) fk ON TRUE
WHERE ns.nspname = $1 AND cls.relname = $2
ORDER BY a.attnum`
	rows, err := db.QueryContext(ctx, query, schema, relation)
	if err != nil {
		return nil, fmt.Errorf("listing postgres columns: %w", err)
	}
	defer rows.Close()
	return scanCatalogColumns(rows, "postgres")
}

func readSQLServerColumns(
	ctx context.Context,
	db *sql.DB,
	schema string,
	relation string,
) ([]Column, error) {
	const query = `SELECT
    col.name,
    TYPE_NAME(col.user_type_id),
    col.is_nullable,
    COALESCE(def.definition, ''),
    col.column_id,
    CASE WHEN col.max_length < 0 THEN NULL ELSE CONVERT(bigint, col.max_length) END,
    CONVERT(bigint, col.precision),
    CONVERT(bigint, col.scale),
    CASE WHEN pk.column_id IS NULL THEN 0 ELSE 1 END,
    fk.reference_schema,
    fk.reference_table,
    fk.reference_column
FROM sys.objects obj
JOIN sys.schemas sch ON sch.schema_id = obj.schema_id
JOIN sys.columns col ON col.object_id = obj.object_id
LEFT JOIN sys.default_constraints def
    ON def.parent_object_id = col.object_id AND def.parent_column_id = col.column_id
OUTER APPLY (
    SELECT TOP (1) ic.column_id
    FROM sys.indexes idx
    JOIN sys.index_columns ic ON ic.object_id = idx.object_id AND ic.index_id = idx.index_id
    WHERE idx.object_id = col.object_id AND idx.is_primary_key = 1 AND ic.column_id = col.column_id
) pk
OUTER APPLY (
    SELECT TOP (1)
        ref_sch.name AS reference_schema,
        ref_obj.name AS reference_table,
        ref_col.name AS reference_column
    FROM sys.foreign_key_columns fkc
    JOIN sys.objects ref_obj ON ref_obj.object_id = fkc.referenced_object_id
    JOIN sys.schemas ref_sch ON ref_sch.schema_id = ref_obj.schema_id
    JOIN sys.columns ref_col
        ON ref_col.object_id = fkc.referenced_object_id AND ref_col.column_id = fkc.referenced_column_id
    WHERE fkc.parent_object_id = col.object_id AND fkc.parent_column_id = col.column_id
) fk
WHERE sch.name = @p1 AND obj.name = @p2 AND obj.type IN ('U', 'V')
ORDER BY col.column_id`
	rows, err := db.QueryContext(ctx, query, schema, relation)
	if err != nil {
		return nil, fmt.Errorf("listing sql server columns: %w", err)
	}
	defer rows.Close()
	return scanCatalogColumns(rows, "sql server")
}

func scanCatalogColumns(rows *sql.Rows, engine string) ([]Column, error) {
	columns := []Column{}
	for rows.Next() {
		var column Column
		var length, precision, scale sql.NullInt64
		var primaryKey bool
		var referenceSchema, referenceTable, referenceColumn sql.NullString
		if err := rows.Scan(
			&column.Name,
			&column.Type,
			&column.Nullable,
			&column.Default,
			&column.Ordinal,
			&length,
			&precision,
			&scale,
			&primaryKey,
			&referenceSchema,
			&referenceTable,
			&referenceColumn,
		); err != nil {
			return nil, fmt.Errorf("scanning %s column: %w", engine, err)
		}
		column.Length, column.HasLength = length.Int64, length.Valid
		column.Precision = precision.Int64
		column.Scale = scale.Int64
		column.HasDecimalSize = precision.Valid
		column.ReferenceSchema = referenceSchema.String
		column.ReferenceTable = referenceTable.String
		column.ReferenceColumn = referenceColumn.String
		column.Key = columnKey(primaryKey, referenceTable.Valid)
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating %s columns: %w", engine, err)
	}
	return columns, nil
}

func columnKey(primaryKey, foreignKey bool) string {
	switch {
	case primaryKey && foreignKey:
		return "PK/FK"
	case primaryKey:
		return "PK"
	case foreignKey:
		return "FK"
	default:
		return ""
	}
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
