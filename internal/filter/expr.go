// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter

import (
	"io"
)

// Expr is the parsed AST of a filter expression.
type Expr interface {
	filterExpr() // not used; restricts Expr to types defined here.

	String() string       // returns a multi-line string representation
	print(io.Writer, int) // used for String
}

// binaryExpr is a binary expression.
type binaryExpr struct {
	op    tokenKind // either tokenAND or tokenOR
	left  Expr
	right Expr
	pos   position // position of op
}

// unaryExpr is a unary expression.
type unaryExpr struct {
	op   tokenKind // either tokenMinus or tokenNot
	expr Expr
	pos  position // position of op
}

// comparisonExpr is a comparison expression.
type comparisonExpr struct {
	op    tokenKind // tokenLessThanEquals, tokenLessThan, and so forth
	left  Expr
	right Expr
	pos   position // position of op
}

// nameExpr is a simple name as an expression.
type nameExpr struct {
	name        string
	pos         position
	isString    bool // true if quoted string used as literal
	isComposite bool // true if in a composite expression
}

// memberExpr is an expression describing the member of some object.
type memberExpr struct {
	holder Expr
	member string
	pos    position // position of dot
}

// functionExpr is a function call expression.
type functionExpr struct {
	fn   Expr
	args []Expr
}

// Indicate that all expression types implement [Expr].

func (*binaryExpr) filterExpr()     {}
func (*unaryExpr) filterExpr()      {}
func (*comparisonExpr) filterExpr() {}
func (*nameExpr) filterExpr()       {}
func (*memberExpr) filterExpr()     {}
func (*functionExpr) filterExpr()   {}
