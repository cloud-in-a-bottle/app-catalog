package store

import (
	"fmt"
	"strings"
)

// parse any logic expression for filtering categories / tags
// exprToSQL converts a boolean logic expression over JSON array membership
// into a SQL fragment and positional placeholder args.
//
// Supported syntax:
//
//	expr  = or_expr
//	or    = and_expr ( ("||" | "|") and_expr )*
//	and   = atom    ( ("&&" | "&") atom )*
//	atom  = "(" expr ")" | IDENT
//	IDENT = [a-zA-Z0-9_-]+  (case-insensitive; lowercased before matching)
//
// Example: "Privacy | Search && AI" → "(LOWER(col) LIKE ? OR (LOWER(col) LIKE ? AND LOWER(col) LIKE ?))"
// Returns ("", nil, nil) for a blank expression.
func exprToSQL(expr, col string) (string, []any, error) {
	tokens, err := tokenizeExpr(expr)
	if err != nil {
		return "", nil, err
	}
	if len(tokens) == 0 {
		return "", nil, nil
	}
	p := &exprParser{tokens: tokens, col: col}
	sql, args, err := p.parseOr()
	if err != nil {
		return "", nil, err
	}
	if p.pos != len(p.tokens) {
		return "", nil, fmt.Errorf("unexpected token %q", p.tokens[p.pos])
	}
	return sql, args, nil
}

type exprParser struct {
	tokens []string
	pos    int
	col    string
}

func (p *exprParser) parseOr() (string, []any, error) {
	lsql, largs, err := p.parseAnd()
	if err != nil {
		return "", nil, err
	}
	for p.pos < len(p.tokens) && p.tokens[p.pos] == "||" {
		p.pos++
		rsql, rargs, err := p.parseAnd()
		if err != nil {
			return "", nil, err
		}
		lsql = "(" + lsql + " OR " + rsql + ")"
		largs = append(largs, rargs...)
	}
	return lsql, largs, nil
}

func (p *exprParser) parseAnd() (string, []any, error) {
	lsql, largs, err := p.parseAtom()
	if err != nil {
		return "", nil, err
	}
	for p.pos < len(p.tokens) && p.tokens[p.pos] == "&&" {
		p.pos++
		rsql, rargs, err := p.parseAtom()
		if err != nil {
			return "", nil, err
		}
		lsql = "(" + lsql + " AND " + rsql + ")"
		largs = append(largs, rargs...)
	}
	return lsql, largs, nil
}

func (p *exprParser) parseAtom() (string, []any, error) {
	if p.pos >= len(p.tokens) {
		return "", nil, fmt.Errorf("unexpected end of expression")
	}
	tok := p.tokens[p.pos]
	if tok == "(" {
		p.pos++
		sql, args, err := p.parseOr()
		if err != nil {
			return "", nil, err
		}
		if p.pos >= len(p.tokens) || p.tokens[p.pos] != ")" {
			return "", nil, fmt.Errorf("expected closing parenthesis")
		}
		p.pos++
		return sql, args, nil
	}
	if tok == "||" || tok == "&&" || tok == ")" {
		return "", nil, fmt.Errorf("unexpected operator %q", tok)
	}
	p.pos++
	return "LOWER(" + p.col + ") LIKE ?", []any{`%"` + tok + `"%`}, nil
}

func tokenizeExpr(expr string) ([]string, error) {
	var tokens []string
	i := 0
	for i < len(expr) {
		c := expr[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		// Two-char operators checked before single-char.
		if i+1 < len(expr) && expr[i:i+2] == "||" {
			tokens = append(tokens, "||")
			i += 2
			continue
		}
		if i+1 < len(expr) && expr[i:i+2] == "&&" {
			tokens = append(tokens, "&&")
			i += 2
			continue
		}
		// Single-char operators normalized to double form.
		if c == '|' {
			tokens = append(tokens, "||")
			i++
			continue
		}
		if c == '&' {
			tokens = append(tokens, "&&")
			i++
			continue
		}
		if c == '(' || c == ')' {
			tokens = append(tokens, string(c))
			i++
			continue
		}
		j := i
		for j < len(expr) && isExprIdentChar(expr[j]) {
			j++
		}
		if j == i {
			return nil, fmt.Errorf("unexpected character %q in expression", c)
		}
		tokens = append(tokens, strings.ToLower(expr[i:j]))
		i = j
	}
	return tokens, nil
}

func isExprIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '-' || c == '_'
}
