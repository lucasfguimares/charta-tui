// Package sqleditor provides the database-independent core of the SQL editor.
// It deliberately has no dependency on Bubble Tea or database drivers.
package sqleditor

import "fmt"

// Position is a zero-based byte offset and a one-based line/column pair.
type Position struct {
	Offset int
	Line   int
	Column int
}

// Range identifies a half-open region in a SQL document.
type Range struct {
	Start Position
	End   Position
}

// Severity is intentionally ordered from most to least important.
type Severity int

const (
	Error Severity = iota
	Warning
	Info
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "ERROR"
	case Warning:
		return "WARNING"
	default:
		return "INFO"
	}
}

func (s Severity) Marker() string {
	switch s {
	case Error:
		return "!"
	case Warning:
		return "▲"
	default:
		return "•"
	}
}

// Diagnostic is the common representation used by local parsers and drivers.
type Diagnostic struct {
	Severity Severity
	Message  string
	Source   string
	Range    Range
	Code     string
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%s [Ln %d, Col %d] %s", d.Severity, d.Range.Start.Line, d.Range.Start.Column, d.Message)
}

type TokenKind int

const (
	TokenWhitespace TokenKind = iota
	TokenKeyword
	TokenFunction
	TokenString
	TokenNumber
	TokenOperator
	TokenComment
	TokenSchema
	TokenTable
	TokenAlias
	TokenParameter
	TokenDelimitedIdentifier
	TokenIdentifier
	TokenPunctuation
	TokenInvalid
)

func (k TokenKind) String() string {
	return [...]string{
		"whitespace", "keyword", "function", "string", "number", "operator", "comment",
		"schema", "table", "alias", "parameter", "delimited-identifier", "identifier",
		"punctuation", "invalid",
	}[k]
}

// Token points into the original document; Text is never rewritten by lexing.
type Token struct {
	Kind  TokenKind
	Text  string
	Range Range
}

// Analysis is an immutable result suitable for transferring from a worker.
type Analysis struct {
	Version     uint64
	Tokens      []Token
	Diagnostics []Diagnostic
	Statements  []Statement
}

type Statement struct {
	Range Range
	Text  string
}

type KeywordCase int

const (
	KeywordUpper KeywordCase = iota
	KeywordLower
	KeywordPreserve
)

type CommaStyle int

const (
	CommaTrailing CommaStyle = iota
	CommaLeading
)

// FormatOptions controls layout only and never changes literal/identifier text.
type FormatOptions struct {
	IndentSize       int
	UseTabs          bool
	KeywordCase      KeywordCase
	CommaStyle       CommaStyle
	ColumnsOnNewLine bool
	BreakJoin        bool
	BreakBoolean     bool
}

func DefaultFormatOptions() FormatOptions {
	return FormatOptions{
		IndentSize: 4, KeywordCase: KeywordUpper, CommaStyle: CommaTrailing,
		ColumnsOnNewLine: true, BreakJoin: true, BreakBoolean: true,
	}
}
