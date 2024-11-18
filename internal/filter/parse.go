// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter

import (
	"fmt"
	"regexp/syntax"
)

// parseTrace can be set by tests to trace the parser.
var parseTrace bool

// ParseFilter parses a filter expression into an [Expr].
// Empty input returns nil, nil.
// filter = [ expression ] ;
func ParseFilter(filter string) (Expr, error) {
	lex := lexer{
		input: filter,
		nextPos: position{
			line: 1,
			col:  0,
		},
	}

	tok := lex.nextToken()
	if tok.kind == tokenWSC {
		tok = lex.nextToken()
	}
	if tok.kind == tokenEOF {
		return nil, nil
	}
	lex.pushToken(tok)

	expr, err := parseExpression(&lex)
	if err != nil {
		return nil, err
	}

	tok = lex.nextToken()
	if tok.kind == tokenWSC {
		tok = lex.nextToken()
	}

	if tok.kind != tokenEOF {
		return nil, fmt.Errorf("%s: unexpected tokens after filter expression", tok.pos)
	}

	return expr, nil
}

// parseExpr parses an expression.
// expression =  sequence, { "AND", sequence } ;
func parseExpression(lex *lexer) (e Expr, err error) {
	fn := trace("Expression")
	defer func() { fn(e, err) }()
	return parseJunction(lex, tokenAnd, parseSequence)
}

// parseSequence parses an implicit conjunction sequence.
// sequence = factor, { factor } ;
func parseSequence(lex *lexer) (e Expr, err error) {
	fn := trace("Sequence")
	defer func() { fn(e, err) }()

	factor, err := parseFactor(lex)
	if err != nil {
		return nil, err
	}

	for {
		tok := lex.nextToken()
		if tok.kind == tokenWSC {
			tok = lex.nextToken()
		}
		lex.pushToken(tok)
		if tok.kind == tokenEOF || tok.kind == tokenRparen {
			return factor, nil
		}

		pos := tok.pos

		if tok.kind == tokenAnd {
			return factor, nil
		}

		rfactor, err := parseFactor(lex)
		if err != nil {
			return nil, err
		}

		factor = &binaryExpr{
			op:    tokenAnd,
			left:  factor,
			right: rfactor,
			pos:   pos,
		}
	}
}

// parseFactor parses a disjunction.
// factor = term, { "OR", term } ;
func parseFactor(lex *lexer) (e Expr, err error) {
	fn := trace("Factor")
	defer func() { fn(e, err) }()
	return parseJunction(lex, tokenOr, parseTerm)
}

// parseJunction parses an AND or OR sequence.
func parseJunction(lex *lexer, kind tokenKind, parseElement func(lex *lexer) (Expr, error)) (e Expr, err error) {
	fn := trace("Junction")
	defer func() { fn(e, err) }()

	sub, err := parseElement(lex)
	if err != nil {
		return nil, err
	}

	for {
		tok := lex.nextToken()
		if tok.kind == tokenWSC {
			tok = lex.nextToken()
		}

		if tok.kind != kind {
			lex.pushToken(tok)
			return sub, nil
		}

		pos := tok.pos

		tok = lex.nextToken()
		if tok.kind != tokenWSC {
			lex.pushToken(tok)
			name := "AND"
			if kind == tokenOr {
				name = "OR"
			}
			return nil, fmt.Errorf("%s: missing whitespace after %s", pos, name)
		}

		rsub, err := parseElement(lex)
		if err != nil {
			return nil, err
		}

		sub = &binaryExpr{
			op:    kind,
			left:  sub,
			right: rsub,
			pos:   pos,
		}
	}
}

// parseTerm parses a term.
// term = [ "-" | "NOT" ], primitive ;
func parseTerm(lex *lexer) (e Expr, err error) {
	fn := trace("Term")
	defer func() { fn(e, err) }()

	tok := lex.nextToken()
	pos := tok.pos
	if tok.kind != tokenMinus && tok.kind != tokenNot {
		lex.pushToken(tok)
	}

	if tok.kind == tokenNot {
		stok := lex.nextToken()
		if stok.kind != tokenWSC {
			lex.pushToken(stok)
			return nil, fmt.Errorf("%s: missing whitespace after NOT", tok.pos)
		}
	}

	var prim Expr

	ntok := lex.nextToken()
	lex.pushToken(ntok)
	if ntok.kind == tokenMinus || ntok.kind == tokenNot {
		prim, err = parseTerm(lex)
	} else {
		prim, err = parsePrimitive(lex)
	}
	if err != nil {
		return nil, err
	}

	if tok.kind == tokenMinus || tok.kind == tokenNot {
		prim = &unaryExpr{
			op:   tok.kind,
			expr: prim,
			pos:  pos,
		}
	}

	return prim, nil
}

// parsePrimitive parses a primitive.
//
//	primitive
//	   = parenthesized expression
//	   | global restriction
//	   | regular restriction
//	   ;
//	global restriction = function | value ;
//	regular restriction = comparable, comparison, argument ;
//	value = unquoted text | quoted string ;
func parsePrimitive(lex *lexer) (e Expr, err error) {
	fn := trace("Primitive")
	defer func() { fn(e, err) }()

	tok := lex.nextToken()
	if tok.kind == tokenWSC {
		tok = lex.nextToken()
	}
	if tok.kind == tokenLparen {
		return parseParenthesizedExpression(lex)
	}

	left, err := parseComparable(lex, tok)
	if err != nil {
		return nil, err
	}

	tok = lex.nextToken()
	if tok.kind == tokenWSC {
		tok = lex.nextToken()
	}

	switch tok.kind {
	case tokenLessThanEquals, tokenLessThan,
		tokenGreaterThanEquals, tokenGreaterThan,
		tokenNotEquals, tokenEquals,
		tokenHas,
		tokenMatchesRegexp, tokenNotMatchesRegexp:

		ntok := lex.nextToken()
		if ntok.kind != tokenWSC {
			lex.pushToken(ntok)
		}

		right, err := parseArg(lex)
		if err != nil {
			return nil, err
		}

		switch tok.kind {
		case tokenMatchesRegexp, tokenNotMatchesRegexp:
			// Verify that the right hand side
			// is a valid regular expression
			// (or a combination of regular expressions,
			// as in "x*" OR "y*").

			var walk func(Expr) error
			walk = func(e Expr) error {
				switch e := e.(type) {
				case *binaryExpr:
					if err := walk(e.left); err != nil {
						return err
					}
					return walk(e.right)
				case *unaryExpr:
					return walk(e.expr)
				case *nameExpr:
					if !e.isString {
						return fmt.Errorf("%s: regular expression is not a quoted string", e.pos)
					}
					if _, err := syntax.Parse(e.name, syntax.Perl); err != nil {
						return fmt.Errorf("%s: invalid regular expression: %v", e.pos, err)
					}
					return nil
				case *comparisonExpr:
					return fmt.Errorf("%s: regular expression is not a quoted string", e.pos)
				case *memberExpr:
					return fmt.Errorf("%s: regular expression is not a quoted string", e.pos)
				case *functionExpr:
					return fmt.Errorf("%s: regular expression is not a quoted string", ntok.pos)
				default:
					panic("can't happen")
				}
			}

			if err := walk(right); err != nil {
				return nil, err
			}
		}

		ret := &comparisonExpr{
			op:    tok.kind,
			left:  left,
			right: right,
			pos:   tok.pos,
		}
		return ret, nil

	default:
		lex.pushToken(tok)
		return left, nil
	}

}

// parseParenthesizedExpressions parses parentheses.
// We've already seen the "(".
// parenthesized expression = "(", expression, ")" ;
func parseParenthesizedExpression(lex *lexer) (e Expr, err error) {
	fn := trace("ParenthesizedExpression")
	defer func() { fn(e, err) }()

	tok := lex.nextToken()
	if tok.kind != tokenWSC {
		lex.pushToken(tok)
	}

	ret, err := parseExpression(lex)
	if err != nil {
		return nil, err
	}

	// For some reason strings in composite expressions are handled
	// differently from plain strings.
	var markComposite func(Expr)
	markComposite = func(e Expr) {
		switch e := e.(type) {
		case *binaryExpr:
			markComposite(e.left)
			markComposite(e.right)
		case *unaryExpr:
			markComposite(e.expr)
		case *comparisonExpr:
			markComposite(e.left)
			markComposite(e.right)
		case *nameExpr:
			e.isComposite = true
		case *memberExpr:
			markComposite(e.holder)
		case *functionExpr:
		default:
			panic("can't happen")
		}
	}
	markComposite(ret)

	tok = lex.nextToken()
	if tok.kind != tokenRparen {
		lex.pushToken(tok)
		return nil, fmt.Errorf("%s: expected right parenthesis after expression", tok.pos)
	}
	return ret, nil
}

// parseComparable parses a value that may be compared.
// comparable = member | function ;
func parseComparable(lex *lexer, tok token) (e Expr, err error) {
	fn := trace("Comparable")
	defer func() { fn(e, err) }()

	// The implemented parser doesn't quite match the documented parser.
	// The implemented parser permits using AND/OR/NOT as a comparable
	// if they are followed and/or preceded by DOT.

	sawWhite := false
	ntok := lex.nextToken()
	if ntok.kind == tokenWSC {
		sawWhite = true
		ntok = lex.nextToken()
	}
	lex.pushToken(ntok)

	switch tok.kind {
	case tokenString, tokenText:
	case tokenAnd, tokenOr, tokenNot:
		if ntok.kind != tokenDot {
			return nil, fmt.Errorf("%s: misplaced AND/OR/NOT", tok.pos)
		}
		tok = keywordToText(tok)
	default:
		return nil, fmt.Errorf("%s: expected identifier or value", tok.pos)
	}

	switch ntok.kind {
	case tokenDot:
		lex.nextToken()
		e, err := parseMember(lex, tok, ntok.pos)
		if err != nil {
			return nil, err
		}

		// Using a member as a function call doesn't seem to be
		// in the grammar, but the test cases accept it.
		ntok := lex.nextToken()
		if ntok.kind != tokenLparen || sawWhite {
			lex.pushToken(ntok)
			return e, nil
		}

		return parseFunction(lex, e)

	case tokenLparen:
		fn := &nameExpr{
			name:     tok.val,
			pos:      tok.pos,
			isString: false,
		}

		if sawWhite || len(tok.val) == 0 || isDigit(rune(tok.val[0])) {
			return fn, nil
		}

		lex.nextToken()

		return parseFunction(lex, fn)

	default:
		ret := &nameExpr{
			name:     tok.val,
			pos:      tok.pos,
			isString: tok.kind == tokenString,
		}
		return ret, nil
	}
}

// parseMember parses a member reference.
// We have already seen the first identifier, in tok, and a ".".
// member     = identifier, { ".", identifier } ;
// identifier = unquoted text ;
func parseMember(lex *lexer, tok token, pos position) (e Expr, err error) {
	fn := trace("Member")
	defer func() { fn(e, err) }()

	var ret Expr
	ret = &nameExpr{
		name:     tok.val,
		pos:      tok.pos,
		isString: false,
	}

	for {
		ntok := lex.nextToken()
		switch ntok.kind {
		case tokenString, tokenText:
		case tokenAnd, tokenOr, tokenNot:
			ntok = keywordToText(ntok)
		default:
			return nil, fmt.Errorf("%s: expected identifier", ntok.pos)
		}

		ret = &memberExpr{
			holder: ret,
			member: ntok.val,
			pos:    pos,
		}

		ntok = lex.nextToken()
		if ntok.kind != tokenDot {
			lex.pushToken(ntok)
			return ret, nil
		}
	}
}

// parseFunction parses a function call.
// We have already seen the function name, in tok, and a "(".
// function   = identifier, "(", [ argument, { ",", argument } ], ")" ;
func parseFunction(lex *lexer, fn Expr) (e Expr, err error) {
	tfn := trace("Function")
	defer func() { tfn(e, err) }()

	tok := lex.nextToken()
	if tok.kind == tokenRparen {
		ret := &functionExpr{
			fn:   fn,
			args: nil,
		}
		return ret, nil
	}
	lex.pushToken(tok)

	var args []Expr
	for {
		arg, err := parseArg(lex)
		if err != nil {
			return nil, err
		}

		args = append(args, arg)

		tok := lex.nextToken()

		if tok.kind == tokenRparen {
			ret := &functionExpr{
				fn:   fn,
				args: args,
			}
			return ret, nil
		}

		if tok.kind == tokenWSC {
			tok = lex.nextToken()
		}
		if tok.kind != tokenComma {
			return nil, fmt.Errorf("%s: expected comma after function argument", tok.pos)
		}

		tok = lex.nextToken()
		if tok.kind != tokenWSC {
			lex.pushToken(tok)
		}
	}
}

// parseArg parses an argument.
//
//	argument
//	   = comparable
//	   | value
//	   | parenthesized expression
//	   ;
func parseArg(lex *lexer) (e Expr, err error) {
	fn := trace("Arg")
	defer func() { fn(e, err) }()

	tok := lex.nextToken()
	if tok.kind == tokenLparen {
		return parseParenthesizedExpression(lex)
	}
	return parseComparable(lex, tok)
}

// keywordToText turns a keyword token into a text token,
// for cases where a keyword is permitted.
func keywordToText(tok token) token {
	switch tok.kind {
	case tokenAnd:
		return token{kind: tokenText, val: "AND"}
	case tokenOr:
		return token{kind: tokenText, val: "OR"}
	case tokenNot:
		return token{kind: tokenText, val: "NOT"}
	default:
		panic("can't happen")
	}
}

// traceIndent is how far to indent the trace.
var traceIndent int

// trace emits a parse trace. It returns a function to defer.
func trace(fn string) func(Expr, error) {
	if parseTrace {
		fmt.Printf("%*s%s\n", traceIndent, "", fn)
		traceIndent++
		return func(e Expr, err error) {
			traceIndent--
			fmt.Printf("%*s%s returning ", traceIndent, "", fn)
			if e == nil && err == nil {
				fmt.Printf("nil, nil\n")
			} else if e == nil {
				fmt.Printf("error %v\n", err)
			} else if err == nil {
				fmt.Printf("%s", e)
			} else {
				fmt.Printf("error %v %s", err, e)
			}
		}
	}
	return func(Expr, error) {}
}
