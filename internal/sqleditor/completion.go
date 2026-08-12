package sqleditor

import (
	"sort"
	"strings"
)

// Catalog is an immutable, driver-neutral metadata snapshot used by
// completion workers. It can safely be copied between Bubble Tea messages.
type Catalog struct {
	DefaultSchema string
	Schemas       []SchemaMetadata
}

type SchemaMetadata struct {
	Name      string
	Relations []RelationMetadata
}

type RelationMetadata struct {
	Schema  string
	Name    string
	Kind    string
	Columns []ColumnMetadata
}

type ColumnMetadata struct {
	Name         string
	SQLType      string
	IsNullable   bool
	HasNullable  bool
	IsPrimaryKey bool
	IsForeignKey bool
}

type CompletionKind int

const (
	CompletionUnknown CompletionKind = iota
	CompletionSchema
	CompletionTable
	CompletionView
	CompletionColumn
	CompletionAlias
	CompletionCTE
	CompletionFunction
	CompletionKeyword
)

func (k CompletionKind) String() string {
	switch k {
	case CompletionSchema:
		return "SCHEMA"
	case CompletionTable:
		return "TABLE"
	case CompletionView:
		return "VIEW"
	case CompletionColumn:
		return "COLUMN"
	case CompletionAlias:
		return "ALIAS"
	case CompletionCTE:
		return "CTE"
	case CompletionFunction:
		return "FUNCTION"
	case CompletionKeyword:
		return "KEYWORD"
	default:
		return "UNKNOWN"
	}
}

type CompletionItem struct {
	Label        string
	InsertText   string
	Kind         CompletionKind
	SQLType      string
	IsNullable   bool
	HasNullable  bool
	IsPrimaryKey bool
	IsForeignKey bool
	MatchStart   int
	MatchLength  int
	priority     int
}

type CompletionResult struct {
	Replace Range
	Prefix  string
	Items   []CompletionItem
}

type queryScope struct {
	selectAt int
	depth    int
	start    int
	end      int
	sources  []querySource
	ctes     map[string]querySource
}

type querySource struct {
	name    string
	alias   string
	columns []ColumnMetadata
	kind    CompletionKind
}

type completionContext int

const (
	contextExpression completionContext = iota
	contextRelation
)

// Complete parses the statement around cursor and resolves its query scope
// against a metadata snapshot. The parser is deliberately tolerant of an
// incomplete statement because completion normally runs while SQL is invalid.
func Complete(sql string, cursor int, dialect Dialect, catalog Catalog) CompletionResult {
	cursor = min(max(cursor, 0), len(sql))
	tokens, _ := (Lexer{Dialect: dialect}).Lex(sql)
	sig := significantIndexes(tokens)
	depths, pairs := tokenDepths(tokens, sig)
	replace, prefix, current := completionPrefix(sql, cursor, tokens, sig)
	scope := buildActiveScope(tokens, sig, depths, pairs, cursor, catalog)
	context := contextAt(tokens, sig, depths, scope, cursor)
	qualifier := qualifierAt(tokens, sig, current, cursor)

	items := make([]CompletionItem, 0, 64)
	if qualifier != "" {
		if context == contextRelation {
			items = append(items, schemaRelations(catalog, qualifier)...)
		} else {
			items = append(items, qualifiedColumns(scope, qualifier)...)
		}
		return rankCompletions(replace, prefix, items)
	}

	if context == contextRelation {
		for _, schema := range catalog.Schemas {
			items = append(items, CompletionItem{Label: schema.Name, InsertText: schema.Name, Kind: CompletionSchema, priority: 20})
			for _, relation := range schema.Relations {
				kind := CompletionTable
				if strings.Contains(strings.ToUpper(relation.Kind), "VIEW") || strings.EqualFold(relation.Kind, "view") {
					kind = CompletionView
				}
				priority := 10
				if catalog.DefaultSchema != "" && strings.EqualFold(schema.Name, catalog.DefaultSchema) {
					priority = 5
				}
				items = append(items, CompletionItem{Label: relation.Name, InsertText: relation.Name, Kind: kind, priority: priority})
			}
		}
		for _, cte := range scope.ctes {
			items = append(items, CompletionItem{Label: cte.name, InsertText: cte.name, Kind: CompletionCTE})
		}
		return rankCompletions(replace, prefix, items)
	}

	for _, source := range scope.sources {
		if source.alias != "" {
			items = append(items, CompletionItem{Label: source.alias, InsertText: source.alias, Kind: CompletionAlias, priority: 10})
		}
		for _, column := range source.columns {
			items = append(items, columnCompletion(column, 0))
		}
	}
	keywords, functions := Vocabulary(dialect)
	for _, function := range functions {
		items = append(items, CompletionItem{Label: function, InsertText: function, Kind: CompletionFunction, priority: 30})
	}
	atStatementStart := completionAtStatementStart(tokens, sig, replace.Start.Offset)
	for _, keyword := range keywords {
		priority := 50
		if atStatementStart && statementStartingKeyword(keyword) {
			priority = 5
		}
		items = append(items, CompletionItem{Label: keyword, InsertText: keyword, Kind: CompletionKeyword, priority: priority})
	}
	return rankCompletions(replace, prefix, items)
}

func completionAtStatementStart(tokens []Token, sig []int, offset int) bool {
	hasToken := false
	for _, index := range sig {
		token := tokens[index]
		if token.Range.Start.Offset >= offset {
			break
		}
		if token.Text == ";" {
			hasToken = false
			continue
		}
		hasToken = true
	}
	return !hasToken
}

func statementStartingKeyword(value string) bool {
	switch strings.ToUpper(value) {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "CREATE", "ALTER", "DROP", "MERGE", "TRUNCATE", "EXPLAIN", "DECLARE", "BEGIN", "CALL", "EXEC", "EXECUTE", "PRAGMA", "VACUUM":
		return true
	default:
		return false
	}
}

func tokenDepths(tokens []Token, sig []int) ([]int, map[int]int) {
	depths := make([]int, len(sig))
	pairs := map[int]int{}
	stack := []int{}
	depth := 0
	for position, index := range sig {
		if tokens[index].Text == ")" {
			depth = max(0, depth-1)
			if len(stack) > 0 {
				open := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				pairs[open], pairs[position] = position, open
			}
		}
		depths[position] = depth
		if tokens[index].Text == "(" {
			stack = append(stack, position)
			depth++
		}
	}
	return depths, pairs
}

func completionPrefix(sql string, cursor int, tokens []Token, sig []int) (Range, string, int) {
	position := PositionAt(sql, cursor)
	replace := Range{Start: position, End: position}
	current := len(sig)
	for p, index := range sig {
		token := tokens[index]
		if cursor < token.Range.Start.Offset {
			current = p
			break
		}
		if cursor >= token.Range.Start.Offset && cursor <= token.Range.End.Offset &&
			(token.Kind == TokenIdentifier || token.Kind == TokenDelimitedIdentifier || token.Kind == TokenKeyword || token.Kind == TokenFunction || token.Kind == TokenTable || token.Kind == TokenSchema || token.Kind == TokenAlias) {
			start := token.Range.Start.Offset
			replace = Range{Start: PositionAt(sql, start), End: token.Range.End}
			return replace, normalizedIdentifier(sql[start:cursor]), p
		}
		current = p + 1
	}
	return replace, "", current
}

func buildActiveScope(tokens []Token, sig, depths []int, pairs map[int]int, cursor int, catalog Catalog) queryScope {
	candidates := []queryScope{}
	for p, index := range sig {
		if !strings.EqualFold(tokens[index].Text, "SELECT") {
			continue
		}
		start, end := scopeBounds(tokens, sig, depths, pairs, p)
		if cursor >= start && cursor <= end {
			candidates = append(candidates, queryScope{selectAt: p, depth: depths[p], start: start, end: end})
		}
	}
	if len(candidates) == 0 {
		return queryScope{start: 0, end: lenTokenText(tokens), ctes: map[string]querySource{}}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].depth != candidates[j].depth {
			return candidates[i].depth < candidates[j].depth
		}
		return candidates[i].selectAt < candidates[j].selectAt
	})
	resolved := queryScope{ctes: map[string]querySource{}}
	for _, candidate := range candidates {
		localCTEs := parseCTEs(tokens, sig, depths, pairs, candidate, catalog)
		candidate.ctes = make(map[string]querySource, len(resolved.ctes)+len(localCTEs))
		if candidate.depth > resolved.depth && candidate.selectAt > resolved.selectAt {
			for name, cte := range resolved.ctes {
				candidate.ctes[name] = cte
			}
		}
		for name, cte := range localCTEs {
			candidate.ctes[name] = cte
		}
		candidate.sources = parseSources(tokens, sig, depths, pairs, candidate, catalog, candidate.ctes)
		if candidate.depth > resolved.depth && candidate.selectAt > resolved.selectAt {
			candidate.sources = append(candidate.sources, resolved.sources...)
		}
		resolved = candidate
	}
	return resolved
}

func scopeBounds(tokens []Token, sig, depths []int, pairs map[int]int, selectAt int) (int, int) {
	start, end := 0, lenTokenText(tokens)
	for p := selectAt - 1; p >= 0; p-- {
		if depths[p] < depths[selectAt] {
			if tokens[sig[p]].Text == "(" {
				start = tokens[sig[p]].Range.End.Offset
				if close, ok := pairs[p]; ok {
					end = tokens[sig[close]].Range.Start.Offset
				}
			}
			break
		}
		if depths[p] == depths[selectAt] && (tokens[sig[p]].Text == ";" || strings.EqualFold(tokens[sig[p]].Text, "UNION")) {
			start = tokens[sig[p]].Range.End.Offset
			break
		}
	}
	for p := selectAt + 1; p < len(sig); p++ {
		if depths[p] < depths[selectAt] {
			break
		}
		if depths[p] == depths[selectAt] && (tokens[sig[p]].Text == ";" || strings.EqualFold(tokens[sig[p]].Text, "UNION")) {
			end = tokens[sig[p]].Range.Start.Offset
			break
		}
	}
	return start, end
}

func parseCTEs(tokens []Token, sig, depths []int, pairs map[int]int, scope queryScope, catalog Catalog) map[string]querySource {
	result := map[string]querySource{}
	with := -1
	for p := scope.selectAt - 1; p >= 0; p-- {
		if tokens[sig[p]].Range.Start.Offset < scope.start {
			break
		}
		if depths[p] == scope.depth && strings.EqualFold(tokens[sig[p]].Text, "WITH") {
			with = p
			break
		}
	}
	if with < 0 {
		return result
	}
	for p := with + 1; p < scope.selectAt; {
		nameToken := tokens[sig[p]]
		if !isNameToken(nameToken) {
			p++
			continue
		}
		name := normalizedIdentifier(nameToken.Text)
		columns := []ColumnMetadata{}
		p++
		if p < len(sig) && tokens[sig[p]].Text == "(" {
			close := pairs[p]
			for q := p + 1; q < close; q++ {
				if depths[q] == scope.depth+1 && isIdentifierToken(tokens[sig[q]]) {
					columns = append(columns, ColumnMetadata{Name: normalizedIdentifier(tokens[sig[q]].Text)})
				}
			}
			p = close + 1
		}
		if p >= len(sig) || !strings.EqualFold(tokens[sig[p]].Text, "AS") {
			continue
		}
		p++
		if p >= len(sig) || tokens[sig[p]].Text != "(" {
			continue
		}
		close, ok := pairs[p]
		if !ok {
			break
		}
		if len(columns) == 0 {
			columns = projectedColumns(tokens, sig, depths, pairs, p+1, close, catalog, result)
		}
		result[strings.ToLower(name)] = querySource{name: name, alias: name, columns: columns, kind: CompletionCTE}
		p = close + 1
	}
	return result
}

func parseSources(tokens []Token, sig, depths []int, pairs map[int]int, scope queryScope, catalog Catalog, ctes map[string]querySource) []querySource {
	result := []querySource{}
	inFromList := false
	for p := scope.selectAt + 1; p < len(sig); p++ {
		token := tokens[sig[p]]
		if token.Range.Start.Offset >= scope.end {
			break
		}
		if depths[p] != scope.depth {
			continue
		}
		upper := strings.ToUpper(token.Text)
		if upper == "WHERE" || upper == "ON" || upper == "GROUP" || upper == "ORDER" || upper == "HAVING" || upper == "UNION" {
			inFromList = false
		}
		relationStart := upper == "FROM" || upper == "JOIN" || token.Text == "," && inFromList
		if !relationStart {
			continue
		}
		inFromList = upper == "FROM" || token.Text == "," && inFromList
		p++
		if p >= len(sig) {
			break
		}
		source := querySource{}
		if tokens[sig[p]].Text == "(" {
			close, ok := pairs[p]
			if !ok {
				continue
			}
			source.columns = projectedColumns(tokens, sig, depths, pairs, p+1, close, catalog, ctes)
			source.kind = CompletionTable
			p = close + 1
		} else if isIdentifierToken(tokens[sig[p]]) {
			first := normalizedIdentifier(tokens[sig[p]].Text)
			schema, name := "", first
			if p+2 < len(sig) && tokens[sig[p+1]].Text == "." && isIdentifierToken(tokens[sig[p+2]]) {
				schema, name, p = first, normalizedIdentifier(tokens[sig[p+2]].Text), p+2
			}
			source.name = name
			if cte, ok := ctes[strings.ToLower(name)]; ok && schema == "" {
				source = cte
			} else if relation, ok := findRelation(catalog, schema, name); ok {
				source.name, source.columns = relation.Name, relation.Columns
				source.kind = CompletionTable
				if strings.Contains(strings.ToUpper(relation.Kind), "VIEW") {
					source.kind = CompletionView
				}
			}
			p++
		} else {
			continue
		}
		if p < len(sig) && strings.EqualFold(tokens[sig[p]].Text, "AS") {
			p++
		}
		if p < len(sig) && isIdentifierToken(tokens[sig[p]]) && !isClauseWord(tokens[sig[p]].Text) {
			source.alias = normalizedIdentifier(tokens[sig[p]].Text)
		} else if source.alias == "" {
			source.alias = source.name
		}
		result = append(result, source)
	}
	return result
}

func projectedColumns(tokens []Token, sig, depths []int, pairs map[int]int, start, end int, catalog Catalog, ctes map[string]querySource) []ColumnMetadata {
	selectAt := -1
	for p := start; p < end; p++ {
		if strings.EqualFold(tokens[sig[p]].Text, "SELECT") {
			selectAt = p
			break
		}
	}
	if selectAt < 0 {
		return nil
	}
	nested := queryScope{selectAt: selectAt, depth: depths[selectAt], start: tokens[sig[start]].Range.Start.Offset, end: tokens[sig[end-1]].Range.End.Offset, ctes: ctes}
	nested.sources = parseSources(tokens, sig, depths, pairs, nested, catalog, ctes)
	columns := []ColumnMetadata{}
	expressionStart := selectAt + 1
	for p := expressionStart; p <= end; p++ {
		boundary := p == end
		if !boundary && depths[p] == nested.depth && (tokens[sig[p]].Text == "," || strings.EqualFold(tokens[sig[p]].Text, "FROM")) {
			boundary = true
		}
		if !boundary {
			continue
		}
		if p > expressionStart {
			piece := sig[expressionStart:p]
			if column, ok := projectionColumn(tokens, piece, nested.sources); ok {
				columns = append(columns, column)
			} else if len(piece) == 1 && tokens[piece[0]].Text == "*" {
				for _, source := range nested.sources {
					columns = append(columns, source.columns...)
				}
			}
		}
		if p < end && strings.EqualFold(tokens[sig[p]].Text, "FROM") {
			break
		}
		expressionStart = p + 1
	}
	return columns
}

func projectionColumn(tokens []Token, indexes []int, sources []querySource) (ColumnMetadata, bool) {
	outputName := ""
	expression := indexes
	for p := len(indexes) - 2; p >= 0; p-- {
		if strings.EqualFold(tokens[indexes[p]].Text, "AS") && isIdentifierToken(tokens[indexes[p+1]]) {
			outputName = normalizedIdentifier(tokens[indexes[p+1]].Text)
			expression = indexes[:p]
			break
		}
	}
	if outputName == "" && len(indexes) > 1 && isIdentifierToken(tokens[indexes[len(indexes)-1]]) && tokens[indexes[len(indexes)-2]].Text != "." {
		outputName = normalizedIdentifier(tokens[indexes[len(indexes)-1]].Text)
		expression = indexes[:len(indexes)-1]
	}
	if len(expression) != 1 && !(len(expression) == 3 && tokens[expression[1]].Text == ".") {
		if outputName != "" {
			return ColumnMetadata{Name: outputName}, true
		}
		return ColumnMetadata{}, false
	}
	columnToken := tokens[expression[len(expression)-1]]
	if !isIdentifierToken(columnToken) {
		return ColumnMetadata{}, false
	}
	columnName := normalizedIdentifier(columnToken.Text)
	if outputName == "" {
		outputName = columnName
	}
	qualifier := ""
	if len(expression) == 3 && isIdentifierToken(tokens[expression[0]]) {
		qualifier = normalizedIdentifier(tokens[expression[0]].Text)
	}
	for _, source := range sources {
		if qualifier != "" && !strings.EqualFold(source.alias, qualifier) && !strings.EqualFold(source.name, qualifier) {
			continue
		}
		for _, column := range source.columns {
			if strings.EqualFold(column.Name, columnName) {
				column.Name = outputName
				return column, true
			}
		}
	}
	return ColumnMetadata{Name: outputName}, true
}

func contextAt(tokens []Token, sig, depths []int, scope queryScope, cursor int) completionContext {
	clause := ""
	for p, index := range sig {
		token := tokens[index]
		if token.Range.Start.Offset >= cursor || depths[p] != scope.depth || token.Range.Start.Offset < scope.start || token.Range.Start.Offset >= scope.end {
			continue
		}
		upper := strings.ToUpper(token.Text)
		switch upper {
		case "SELECT", "WHERE", "ON", "HAVING", "SET", "VALUES", "RETURNING":
			clause = upper
		case "FROM", "JOIN":
			clause = upper
		}
	}
	if clause == "FROM" || clause == "JOIN" {
		return contextRelation
	}
	return contextExpression
}

func qualifierAt(tokens []Token, sig []int, current, cursor int) string {
	p := current - 1
	if current < len(sig) {
		token := tokens[sig[current]]
		if cursor > token.Range.Start.Offset && cursor <= token.Range.End.Offset {
			p = current - 1
		}
	}
	if p >= 1 && tokens[sig[p]].Text == "." && isIdentifierToken(tokens[sig[p-1]]) {
		return normalizedIdentifier(tokens[sig[p-1]].Text)
	}
	return ""
}

func qualifiedColumns(scope queryScope, qualifier string) []CompletionItem {
	for _, source := range scope.sources {
		if strings.EqualFold(source.alias, qualifier) || (source.alias == "" && strings.EqualFold(source.name, qualifier)) {
			items := make([]CompletionItem, 0, len(source.columns))
			for _, column := range source.columns {
				items = append(items, columnCompletion(column, 0))
			}
			return items
		}
	}
	return nil
}

func schemaRelations(catalog Catalog, qualifier string) []CompletionItem {
	for _, schema := range catalog.Schemas {
		if !strings.EqualFold(schema.Name, qualifier) {
			continue
		}
		items := make([]CompletionItem, 0, len(schema.Relations))
		for _, relation := range schema.Relations {
			kind := CompletionTable
			if strings.Contains(strings.ToUpper(relation.Kind), "VIEW") {
				kind = CompletionView
			}
			items = append(items, CompletionItem{Label: relation.Name, InsertText: relation.Name, Kind: kind})
		}
		return items
	}
	return nil
}

func findRelation(catalog Catalog, schema, name string) (RelationMetadata, bool) {
	var fallback RelationMetadata
	found := false
	for _, candidateSchema := range catalog.Schemas {
		if schema != "" && !strings.EqualFold(candidateSchema.Name, schema) {
			continue
		}
		for _, relation := range candidateSchema.Relations {
			if !strings.EqualFold(relation.Name, name) {
				continue
			}
			if schema != "" || catalog.DefaultSchema != "" && strings.EqualFold(candidateSchema.Name, catalog.DefaultSchema) {
				return relation, true
			}
			if !found {
				fallback, found = relation, true
			}
		}
	}
	return fallback, found
}

func columnCompletion(column ColumnMetadata, priority int) CompletionItem {
	return CompletionItem{
		Label: column.Name, InsertText: column.Name, Kind: CompletionColumn, SQLType: column.SQLType,
		IsNullable: column.IsNullable, HasNullable: column.HasNullable, IsPrimaryKey: column.IsPrimaryKey, IsForeignKey: column.IsForeignKey, priority: priority,
	}
}

func rankCompletions(replace Range, prefix string, items []CompletionItem) CompletionResult {
	filtered := make([]CompletionItem, 0, len(items))
	seen := map[string]bool{}
	lowerPrefix := strings.ToLower(prefix)
	for _, item := range items {
		matchByte := strings.Index(strings.ToLower(item.Label), lowerPrefix)
		if lowerPrefix != "" && matchByte < 0 {
			continue
		}
		key := strings.ToLower(item.Label) + "\x00" + item.Kind.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		matchRune := 0
		if matchByte > 0 {
			matchRune = len([]rune(item.Label[:matchByte]))
		}
		item.MatchStart, item.MatchLength = matchRune, len([]rune(prefix))
		if matchByte > 0 {
			item.priority += 100
		}
		filtered = append(filtered, item)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].priority != filtered[j].priority {
			return filtered[i].priority < filtered[j].priority
		}
		return strings.ToLower(filtered[i].Label) < strings.ToLower(filtered[j].Label)
	})
	return CompletionResult{Replace: replace, Prefix: prefix, Items: filtered}
}

func isIdentifierToken(token Token) bool {
	switch token.Kind {
	case TokenIdentifier, TokenDelimitedIdentifier, TokenTable, TokenSchema, TokenAlias:
		return true
	default:
		return false
	}
}

func isNameToken(token Token) bool {
	return isIdentifierToken(token) || token.Kind == TokenFunction
}

func isClauseWord(value string) bool {
	switch strings.ToUpper(value) {
	case "FROM", "JOIN", "LEFT", "RIGHT", "FULL", "INNER", "OUTER", "CROSS", "ON", "WHERE", "GROUP", "ORDER", "HAVING", "LIMIT", "OFFSET", "UNION", "RETURNING", "AS":
		return true
	default:
		return false
	}
}

func lenTokenText(tokens []Token) int {
	if len(tokens) == 0 {
		return 0
	}
	return tokens[len(tokens)-1].Range.End.Offset
}
