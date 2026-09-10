package sqleditor

import "strings"

// Dialect identifies grammar and vocabulary without coupling the editor to a connection type.
type Dialect interface {
	ID() string
	DisplayName() string
	IsKeyword(string) bool
	IsFunction(string) bool
	Supports(string) bool
}

type dialect struct {
	id, name  string
	keywords  map[string]struct{}
	functions map[string]struct{}
	features  map[string]struct{}
}

func (d dialect) ID() string                  { return d.id }
func (d dialect) DisplayName() string         { return d.name }
func (d dialect) IsKeyword(word string) bool  { _, ok := d.keywords[strings.ToUpper(word)]; return ok }
func (d dialect) IsFunction(word string) bool { _, ok := d.functions[strings.ToUpper(word)]; return ok }
func (d dialect) Supports(feature string) bool {
	_, ok := d.features[strings.ToLower(feature)]
	return ok
}

// Vocabulary returns sorted-independent copies of the keywords and functions
// understood by a dialect. Completion sorts and ranks the values for the
// current cursor context.
func Vocabulary(value Dialect) (keywords, functions []string) {
	d, ok := value.(dialect)
	if !ok {
		return nil, nil
	}
	keywords = make([]string, 0, len(d.keywords))
	for keyword := range d.keywords {
		keywords = append(keywords, keyword)
	}
	functions = make([]string, 0, len(d.functions))
	for function := range d.functions {
		functions = append(functions, function)
	}
	return keywords, functions
}

func words(values string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, value := range strings.Fields(values) {
		result[strings.ToUpper(value)] = struct{}{}
	}
	return result
}

func features(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[strings.ToLower(value)] = struct{}{}
	}
	return result
}

const commonKeywords = `SELECT FROM WHERE JOIN LEFT RIGHT FULL INNER OUTER CROSS ON GROUP BY ORDER HAVING
INSERT INTO VALUES UPDATE SET DELETE CREATE ALTER DROP TABLE VIEW INDEX SCHEMA DATABASE WITH RECURSIVE AS
CASE WHEN THEN ELSE END UNION ALL DISTINCT EXISTS IN IS NULL AND OR NOT LIKE BETWEEN ESCAPE ASC DESC LIMIT
OFFSET FETCH FIRST NEXT ROW ROWS ONLY RETURNING OUTPUT OVER PARTITION WINDOW QUALIFY MERGE USING MATCHED
DECLARE BEGIN COMMIT ROLLBACK TRANSACTION PRIMARY KEY FOREIGN REFERENCES UNIQUE CHECK DEFAULT CONSTRAINT
GRANT REVOKE TRUNCATE EXPLAIN ANALYZE REPLACE CONFLICT DO NOTHING PROCEDURE FUNCTION TRIGGER IF ELSEIF WHILE`

const commonFunctions = `COUNT SUM AVG MIN MAX COALESCE NULLIF CAST CONVERT SUBSTRING LENGTH LOWER UPPER TRIM
ROUND ABS CURRENT_DATE CURRENT_TIME CURRENT_TIMESTAMP ROW_NUMBER RANK DENSE_RANK LAG LEAD CONCAT NOW DATEADD
DATEDIFF STRING_AGG ARRAY_AGG JSON_OBJECT JSON_ARRAY EXTRACT STRFTIME IFNULL ISNULL NEWID RANDOM`

func makeDialect(id, name, extraKeywords, extraFunctions string, fs ...string) Dialect {
	ks := words(commonKeywords + " " + extraKeywords)
	fns := words(commonFunctions + " " + extraFunctions)
	return dialect{id: id, name: name, keywords: ks, functions: fns, features: features(fs...)}
}

func ANSI() Dialect { return makeDialect("ansi", "SQL", "", "", "double_quote") }
func TSQL() Dialect {
	return makeDialect("tsql", "T-SQL", `TOP PERCENT TIES APPLY OUTER APPLY GO EXEC EXECUTE TRY CATCH
IDENTITY NOCOUNT NVARCHAR DATETIME2 BIT MONEY`, `GETDATE SYSDATETIME DATEPART CHARINDEX LEN SCOPE_IDENTITY OPENJSON OPENXML STRING_SPLIT`,
		"top", "brackets", "at_parameters", "hash_identifiers")
}
func PostgreSQL() Dialect {
	return makeDialect("postgres", "PostgreSQL", `ILIKE SIMILAR LATERAL MATERIALIZED GENERATED ALWAYS STORED
SERIAL BIGSERIAL BOOLEAN JSONB UUID BYTEA`, `TO_CHAR TO_DATE DATE_TRUNC GENERATE_SERIES JSONB_BUILD_OBJECT`,
		"limit", "returning", "dollar_strings", "colon_cast")
}
func MySQL() Dialect {
	return makeDialect("mysql", "MySQL / MariaDB", `STRAIGHT_JOIN SQL_CALC_FOUND_ROWS DUPLICATE SHOW DESCRIBE
USE UNSIGNED AUTO_INCREMENT ENGINE`, `DATE_FORMAT GROUP_CONCAT LAST_INSERT_ID`,
		"limit", "backticks", "question_parameters")
}
func SQLite() Dialect {
	return makeDialect("sqlite", "SQLite", `PRAGMA ATTACH DETACH WITHOUT ROWID VACUUM GLOB`,
		`JULIANDAY TOTAL_CHANGES LAST_INSERT_ROWID JSON_EACH JSON_TREE PRAGMA_TABLE_INFO`, "limit", "returning", "brackets", "question_parameters")
}

func ByID(id string) Dialect {
	switch strings.ToLower(id) {
	case "sqlserver", "mssql", "tsql":
		return TSQL()
	case "postgres", "postgresql":
		return PostgreSQL()
	case "mysql", "mariadb":
		return MySQL()
	case "sqlite", "sqlite3":
		return SQLite()
	default:
		return ANSI()
	}
}
