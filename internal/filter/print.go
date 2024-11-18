// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// String returns a string representation.
func (e *binaryExpr) String() string     { return exprString(e) }
func (e *unaryExpr) String() string      { return exprString(e) }
func (e *comparisonExpr) String() string { return exprString(e) }
func (e *nameExpr) String() string       { return exprString(e) }
func (e *memberExpr) String() string     { return exprString(e) }
func (e *functionExpr) String() string   { return exprString(e) }

// exprString returns the string representation of an Expr.
func exprString(e Expr) string {
	var sb strings.Builder
	e.print(&sb, 0)
	return sb.String()
}

// print prints an indented representation to w.
func (e *binaryExpr) print(w io.Writer, indent int) {
	fmt.Fprintf(w, "%*s", indent, "")
	switch e.op {
	case tokenAnd:
		io.WriteString(w, "conjunction\n")
	case tokenOr:
		io.WriteString(w, "disjunction\n")
	default:
		panic("can't happen")
	}
	e.left.print(w, indent+2)
	e.right.print(w, indent+2)
}

// print prints an indented representation to w.
func (e *unaryExpr) print(w io.Writer, indent int) {
	fmt.Fprintf(w, "%*s", indent, "")
	switch e.op {
	case tokenMinus:
		io.WriteString(w, "minus\n")
	case tokenNot:
		io.WriteString(w, "not\n")
	default:
		panic("can't happen")
	}
	e.expr.print(w, indent+2)
}

// print prints an indented representation to w.
func (e *comparisonExpr) print(w io.Writer, indent int) {
	fmt.Fprintf(w, "%*scompare %s\n", indent, "", tokenKindStrings[e.op])
	e.left.print(w, indent+2)
	e.right.print(w, indent+2)
}

// print prints an indented representation to w.
func (e *nameExpr) print(w io.Writer, indent int) {
	fmt.Fprintf(w, "%*s", indent, "")
	if e.isString {
		fmt.Fprintf(w, "%q\n", e.name)
	} else {
		fmt.Fprintf(w, "%s\n", e.name)
	}
}

// print prints an indented representation to w.
func (e *memberExpr) print(w io.Writer, indent int) {
	fmt.Fprintf(w, "%*s", indent, "")
	names := []string{e.member}
	par := e
	for sub, ok := par.holder.(*memberExpr); ok; sub, ok = par.holder.(*memberExpr) {
		names = append(names, sub.member)
		par = sub
	}
	names = append(names, par.holder.(*nameExpr).name)
	slices.Reverse(names)
	for i, name := range names {
		if i > 0 {
			io.WriteString(w, ".")
		}
		io.WriteString(w, name)
	}
	io.WriteString(w, "\n")
}

// print prints an indented representation to w.
func (e *functionExpr) print(w io.Writer, indent int) {
	fmt.Fprintf(w, "%*scall", indent, "")
	e.fn.print(w, indent+2)
	for _, arg := range e.args {
		arg.print(w, indent+2)
	}
}
