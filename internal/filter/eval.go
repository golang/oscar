// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Functions is a set of functions that are permitted when filtering.
// Each value in the map must be a function. Each function must
// take at least one argument, which is the type T in Evaluator[T].
// The function may take other arguments, which will come from
// the filter expression. The function should return one or two results;
// the first result is the type that the function returns,
// and the optional second result is type error.
type Functions map[string]any

// Evaluator takes an [Expr] and an arbitrary Go type.
// It returns a function that takes an argument of that Go type
// and reports whether it matches the expression.
// The functions argument contains functions the filter expression may call.
// Evaluator also returns a list of warning messages about the expression.
func Evaluator[T any](e Expr, functions Functions) (func(context.Context, T) bool, []string) {
	ev := eval[T]{
		functions: functions,
	}

	if e == nil {
		fn := func(context.Context, T) bool {
			return true
		}
		return fn, nil
	}

	r := ev.expr(e)
	return r, ev.msgs
}

// eval manages constructing an evaluator function for an [Expr].
type eval[T any] struct {
	functions Functions
	msgs      []string
}

// expr returns an evaluator function for an [Expr].
func (ev *eval[T]) expr(e Expr) func(context.Context, T) bool {
	switch e := e.(type) {
	case *binaryExpr:
		return ev.binary(e)
	case *unaryExpr:
		return ev.unary(e)
	case *comparisonExpr:
		return ev.comparison(e)
	case *nameExpr:
		return ev.name(e)
	case *memberExpr:
		return ev.member(e)
	case *functionExpr:
		return ev.function(e)
	default:
		panic("can't happen")
	}
}

// binary returns an evaluator function for a [binaryExpr].
func (ev *eval[T]) binary(e *binaryExpr) func(context.Context, T) bool {
	left := ev.expr(e.left)
	right := ev.expr(e.right)
	switch e.op {
	case tokenAnd:
		return func(ctx context.Context, v T) bool {
			return left(ctx, v) && right(ctx, v)
		}
	case tokenOr:
		return func(ctx context.Context, v T) bool {
			return left(ctx, v) || right(ctx, v)
		}
	default:
		panic("can't happen")
	}
}

// unary returns an evaluator function for a [unaryExpr].
func (ev *eval[T]) unary(e *unaryExpr) func(context.Context, T) bool {
	sub := ev.expr(e.expr)
	switch e.op {
	case tokenMinus, tokenNot:
		return func(ctx context.Context, v T) bool {
			return !sub(ctx, v)
		}
	default:
		panic("can't happen")
	}
}

// comparison returns an evaluator function for a [comparisonExpr].
func (ev *eval[T]) comparison(e *comparisonExpr) func(context.Context, T) bool {
	if e.op == tokenHas {
		return ev.has(e)
	}

	// Although it's not clearly documented,
	// some existing implementations permit a > 3
	// where a is a slice. This matches if any
	// element of a is > 3. Since supporting this
	// makes the evaluator less efficient and since
	// it is not the common case, handle it specially.
	if multi, _ := ev.isMultiple(e.left); multi {
		return ev.multipleCompare(e)
	}

	left, leftType := ev.fieldValue(e.left)
	if left == nil {
		return ev.alwaysFalse()
	}

	// Treat an equality comparison with a repeated field
	// as a has operation.
	if e.op == tokenEquals || e.op == tokenNotEquals {
		switch leftType.Kind() {
		case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct:
			fn := ev.has(e)
			if e.op == tokenNotEquals {
				return func(ctx context.Context, v T) bool {
					return !fn(ctx, v)
				}
			}
			return fn
		}
	}

	switch e.op {
	case tokenLessThanEquals, tokenLessThan,
		tokenGreaterThanEquals, tokenGreaterThan,
		tokenNotEquals, tokenEquals:

		return ev.compare(e.op, left, leftType, e.right)

	case tokenMatchesRegexp, tokenNotMatchesRegexp:
		if leftType.Kind() != reflect.String {
			ev.msgs = append(ev.msgs, fmt.Sprintf("%s: regular expression match on non-string type %s", e.pos, leftType))
			return ev.alwaysFalse()
		}
		return ev.compare(e.op, left, leftType, e.right)

	default:
		panic("can't happen")
	}
}

// name returns an evaluator function for a [nameExpr].
func (ev *eval[T]) name(e *nameExpr) func(context.Context, T) bool {
	// A literal can be matched anywhere.
	typ := reflect.TypeFor[T]()
	matchFn := ev.match(typ, e)
	if matchFn == nil {
		if typ.Kind() == reflect.Struct {
			ev.msgs = append(ev.msgs, fmt.Sprintf("%s: no fields of value %s match %q", e.pos, typ, e.name))
		} else {
			ev.msgs = append(ev.msgs, fmt.Sprintf("%s: value of type %s can't match %q", e.pos, typ, e.name))
		}
		return ev.alwaysFalse()
	}
	return func(ctx context.Context, v T) bool {
		return matchFn(ctx, reflect.ValueOf(v))
	}
}

// member returns an evaluator function for a [memberExpr].
// This is something like plain "a.b", not in a comparison.
// We accept a boolean value.
// It's not clear that AIP 160 permits this case.
func (ev *eval[T]) member(e *memberExpr) func(context.Context, T) bool {
	valFn, vtyp := ev.memberHolder(e)
	if valFn == nil {
		return ev.alwaysFalse()
	}
	ffn, ftyp := ev.fieldAccessor(e.pos, vtyp, e.member)
	if ffn == nil {
		return ev.alwaysFalse()
	}
	if ftyp.Kind() != reflect.Bool {
		ev.msgs = append(ev.msgs, fmt.Sprintf("%s: reference to field %s of non-boolean type %s", e.pos, e.member, ftyp))
		return ev.alwaysFalse()
	}
	return func(ctx context.Context, v T) bool {
		hval := valFn(ctx, v)
		if !hval.IsValid() {
			return false
		}
		fval := ffn(ctx, hval)
		if !fval.IsValid() {
			return false
		}
		return fval.Bool()
	}
}

// function returns an evaluator function for a [functionExpr].
func (ev *eval[T]) function(*functionExpr) func(context.Context, T) bool {
	ev.msgs = append(ev.msgs, "function not implemented")
	return ev.alwaysFalse()
}

// isMultiple takes the left hand side of a comparison operation
// and returns either true, nil if it contains multiple values,
// or false, type if it does not. An expression contains
// multiple values if it includes a reference to a slice or a map.
func (ev *eval[T]) isMultiple(e Expr) (bool, reflect.Type) {
	isMultipleType := func(typ reflect.Type) bool {
		switch typ.Kind() {
		case reflect.Map, reflect.Slice, reflect.Array:
			return true
		default:
			return false
		}
	}

	switch e := e.(type) {
	case *nameExpr:
		var typ reflect.Type
		if sf, ok := fieldName(reflect.TypeFor[T](), e.name); ok {
			typ = sf.Type
		} else if m, ok := methodName(reflect.TypeFor[T](), e.name); ok {
			typ = m.Type.Out(0)
		} else if cfn, ok := ev.functions[e.name]; ok {
			typ = reflect.TypeOf(cfn).Out(0)
		} else if e.isString {
			typ = reflect.TypeFor[string]()
		} else {
			return false, nil
		}

		if isMultipleType(typ) {
			return true, nil
		}
		return false, typ

	case *memberExpr:
		hmultiple, htype := ev.isMultiple(e.holder)
		if hmultiple {
			return true, nil
		}
		if htype == nil {
			return false, nil
		}

		if sf, ok := fieldName(htype, e.member); ok {
			if isMultipleType(sf.Type) {
				return true, nil
			}
			return false, sf.Type
		}

		if m, ok := methodName(htype, e.member); ok {
			rt := m.Type.Out(0)
			if isMultipleType(rt) {
				return true, nil
			}
			return false, rt
		}

		return false, nil

	case *functionExpr:
		ev.msgs = append(ev.msgs, "isMultiple of functionExpr not implemented")
		return false, nil

	default:
		return false, nil
	}
}

// fieldValue takes the left hand side of a comparison operation.
// It returns a function that evaluates to the value of the field,
// and also returns the type of the field.
// This returns nil, nil on failure.
func (ev *eval[T]) fieldValue(e Expr) (func(context.Context, T) reflect.Value, reflect.Type) {
	switch e := e.(type) {
	case *nameExpr:
		// This should be the name of a field in T.
		// We also permit a function with no extra arguments;
		// this can be used to resolve arbitrary names.
		if _, ok := fieldName(reflect.TypeFor[T](), e.name); ok {
			return ev.topFieldValue(e.pos, e.name)
		}
		if _, ok := methodName(reflect.TypeFor[T](), e.name); ok {
			return ev.topFieldValue(e.pos, e.name)
		}
		if cfn, ok := ev.functions[e.name]; ok {
			return ev.fieldFunction(cfn)
		}
		if e.isString {
			fn := func(context.Context, T) reflect.Value {
				return reflect.ValueOf(e.name)
			}
			return fn, reflect.TypeFor[string]()
		}
		ev.msgs = append(ev.msgs, fmt.Sprintf("%s: unknown literal %q", e.pos, e.name))
		return nil, nil

	case *memberExpr:
		return ev.memberValue(e)

	case *functionExpr:
		return ev.functionValue(e)

	default:
		panic("can't happen")
	}
}

// fieldFunction is called when a function name appears on the
// left hand side of a comparison operation. We evalute the function
// and return a function that evaluates to the value of the field,
// and the type of the value. This is an extension to AIP 160
// that makes it easy for Go code to handle fields that do not
// appear explicitly in T. Returns nil, nil on failure.
func (ev *eval[T]) fieldFunction(fn any) (func(context.Context, T) reflect.Value, reflect.Type) {
	ev.msgs = append(ev.msgs, "fieldFunction not implemented")
	return func(context.Context, T) reflect.Value { return reflect.Value{} }, reflect.TypeFor[int]()
}

// functionValue is called when a function expression appears on
// the left hand side of a comparison operation.
// It returns a function that evaluates to the value of the field,
// and also returns the type of the field.
// This returns nil, nil on failure.
func (ev *eval[T]) functionValue(e *functionExpr) (func(context.Context, T) reflect.Value, reflect.Type) {
	ev.msgs = append(ev.msgs, "functionValue not implemented")
	return func(context.Context, T) reflect.Value { return reflect.Value{} }, reflect.TypeFor[int]()
}

// memberValue returns a funtion that evaluates to a reflect.Value
// of a member expression.
// It also returns the reflect.Type of the value returned by the function.
// It returns nil, nil on failure.
func (ev *eval[T]) memberValue(e *memberExpr) (func(context.Context, T) reflect.Value, reflect.Type) {
	valFn, vtyp := ev.memberHolder(e)
	if valFn == nil {
		return nil, nil
	}
	ffn, ftyp := ev.fieldAccessor(e.pos, vtyp, e.member)
	if ffn == nil {
		return nil, nil
	}
	fn := func(ctx context.Context, v T) reflect.Value {
		hval := valFn(ctx, v)
		if !hval.IsValid() {
			return hval
		}
		return ffn(ctx, hval)
	}
	return fn, ftyp
}

// memberHolder returns a function that evaluates to a reflect.Value
// of the holder part of a member expression, which is the expression
// that appears before the dot (in the expression s.f, the holder is s).
// This function also returns the reflect.Type of the value returned
// by the returned function.
// It returns nil, nil on failure.
func (ev *eval[T]) memberHolder(e *memberExpr) (func(context.Context, T) reflect.Value, reflect.Type) {
	switch holder := e.holder.(type) {
	case *nameExpr:
		// This should be the name of a field in T.
		return ev.topFieldValue(e.pos, holder.name)

	case *memberExpr:
		return ev.memberValue(holder)

	default:
		panic("can't happen")
	}
}

// topFieldValue returns a function that retrieves the value of
// a field name in the type T.
func (ev *eval[T]) topFieldValue(pos position, name string) (func(context.Context, T) reflect.Value, reflect.Type) {
	ffn, ftyp := ev.fieldAccessor(pos, reflect.TypeFor[T](), name)
	if ffn == nil {
		return nil, nil
	}
	fn := func(ctx context.Context, v T) reflect.Value {
		return ffn(ctx, reflect.ValueOf(v))
	}
	return fn, ftyp
}

// fieldAccessor returns a function that retrieves the value of
// the field or method in the name parameter in the type holderType.
// The function takes a reflect.Value that is of type holderType,
// and returns a new reflect.Value.
// fieldAccessor also returns the type of the field or method result.
// If there is no field or method, fieldAccessor returns nil, nil.
func (ev *eval[T]) fieldAccessor(pos position, holderType reflect.Type, name string) (func(context.Context, reflect.Value) reflect.Value, reflect.Type) {
	type vvfunc = func(reflect.Value) reflect.Value
	type cvvfunc = func(context.Context, reflect.Value) reflect.Value
	wrap := func(fn vvfunc) cvvfunc {
		return func(ctx context.Context, v reflect.Value) reflect.Value {
			return fn(v)
		}
	}
	holderTypeOrig := holderType
	if holderType.Kind() == reflect.Pointer {
		holderType = holderType.Elem()
		wrap = func(fn vvfunc) cvvfunc {
			return func(ctx context.Context, v reflect.Value) reflect.Value {
				if !v.IsValid() || v.IsNil() {
					return reflect.Value{}
				}
				return fn(v.Elem())
			}
		}
	}

	var mapErr error
	switch holderType.Kind() {
	case reflect.Struct:
		sf, ok := fieldName(holderType, name)
		if !ok {
			break
		}

		fn := func(v reflect.Value) reflect.Value {
			if !v.IsValid() {
				return v
			}
			fv, _ := v.FieldByIndexErr(sf.Index)
			return fv
		}
		ftype := sf.Type
		if ftype.Kind() != reflect.Pointer {
			return wrap(fn), ftype
		}
		pfn := func(v reflect.Value) reflect.Value {
			v = fn(v)
			if !v.IsValid() || v.IsNil() {
				return reflect.Value{}
			}
			return v.Elem()
		}
		return wrap(pfn), ftype.Elem()

	case reflect.Map:
		var key reflect.Value
		key, mapErr = ev.parseLiteral(pos, holderType.Key(), name)
		if mapErr != nil {
			break
		}

		fn := func(v reflect.Value) reflect.Value {
			if !v.IsValid() {
				return v
			}
			return v.MapIndex(key)
		}
		return wrap(fn), holderType.Elem()

	default:
		// Special case: we can get the "seconds" field of a
		// time.Duration. For flexibility we permit that on
		// any int64 field.
		if strings.EqualFold(name, "seconds") && holderType.Kind() == reflect.Int64 {
			fn := func(v reflect.Value) reflect.Value {
				if !v.IsValid() {
					return v
				}
				secs := time.Duration(v.Int()).Seconds()
				return reflect.ValueOf(secs)
			}
			return wrap(fn), reflect.TypeFor[float64]()
		}
	}

	if m, ok := methodName(holderTypeOrig, name); ok {
		fn := callMethod(holderTypeOrig, m)

		// Dereference pointer results, since there is no
		// way to filter on pointer values.

		rtype := m.Type.Out(0)
		if rtype.Kind() != reflect.Pointer {
			return fn, rtype
		}

		pfn := func(ctx context.Context, v reflect.Value) reflect.Value {
			v = fn(ctx, v)
			if !v.IsValid() || v.IsNil() {
				return reflect.Value{}
			}
			return v.Elem()
		}

		return pfn, rtype.Elem()
	}

	switch holderType.Kind() {
	case reflect.Struct:
		ev.msgs = append(ev.msgs, fmt.Sprintf("%s: %s has no field or method %s", pos, holderType, name))
	case reflect.Map:
		if holderType.NumMethod() > 0 {
			ev.msgs = append(ev.msgs, fmt.Sprintf("%s: %s has no method %q and parsing as map key failed: %v", pos, holderType, name, mapErr))
		} else {
			ev.msgs = append(ev.msgs, mapErr.Error())
		}
	default:
		ev.msgs = append(ev.msgs, fmt.Sprintf("%s: %s has no fields and no method %s", pos, holderType, name))
	}
	return nil, nil
}

// compare returns a function that returns the result of a
// comparison of a left value and a right value. The left value is
// a reflect.Value returned by left, and has type leftType.
// The right value is an expression, which may not be a field but may
// be a logical expression with AND and OR.
func (ev *eval[T]) compare(op tokenKind, left func(context.Context, T) reflect.Value, leftType reflect.Type, right Expr) func(context.Context, T) bool {
	cfn := ev.compareVal(op, leftType, right)
	if cfn == nil {
		return ev.alwaysFalse()
	}
	return func(ctx context.Context, v T) bool {
		return cfn(ctx, left(ctx, v))
	}
}

// compareVal returns a function that takes a reflect.Value of type leftType
// and returns the result of a comparison of that value with right.
// The right value is an expression, which may not be a field but may
// be a logical expression with AND and OR. This returns nil on error.
func (ev *eval[T]) compareVal(op tokenKind, leftType reflect.Type, right Expr) func(context.Context, reflect.Value) bool {
	switch right := right.(type) {
	case *binaryExpr:
		lf := ev.compareVal(op, leftType, right.left)
		rf := ev.compareVal(op, leftType, right.right)
		if lf == nil || rf == nil {
			return nil
		}
		switch right.op {
		case tokenAnd:
			return func(ctx context.Context, v reflect.Value) bool {
				return lf(ctx, v) && rf(ctx, v)
			}
		case tokenOr:
			return func(ctx context.Context, v reflect.Value) bool {
				return lf(ctx, v) || rf(ctx, v)
			}
		default:
			panic("can't happen")
		}

	case *unaryExpr:
		uf := ev.compareVal(op, leftType, right.expr)
		if uf == nil {
			return nil
		}
		switch right.op {
		case tokenMinus, tokenNot:
			return func(ctx context.Context, v reflect.Value) bool {
				return !uf(ctx, v)
			}
		default:
			panic("can't happen")
		}

	case *nameExpr:
		fn := ev.compareName(op, leftType, right)
		return func(ctx context.Context, v reflect.Value) bool {
			return fn(v)
		}

	case *functionExpr:
		return ev.compareFunction(op, leftType, right)

	case *memberExpr:
		// An a.b on the right of a comparison is just the
		// string "a.b".
		var stringifyHolder func(e *memberExpr) string
		stringifyHolder = func(e *memberExpr) string {
			switch h := e.holder.(type) {
			case *nameExpr:
				return h.name
			case *memberExpr:
				return stringifyHolder(h) + "." + h.member
			default:
				panic("can't happen")
			}
		}
		name := stringifyHolder(right) + "." + right.member
		ne := &nameExpr{
			name: name,
			pos:  right.pos,
		}
		fn := ev.compareName(op, leftType, ne)
		return func(ctx context.Context, v reflect.Value) bool {
			return fn(v)
		}

	case *comparisonExpr:
		ev.msgs = append(ev.msgs, fmt.Sprintf("%s: invalid comparison on right of comparison", right.pos))
		return nil

	default:
		panic("can't happen")
	}
}

// compareName returns a function that takes a reflect.Value of type leftType
// and returns the result of a comparison of that value with right.
// This returns nil on error.
func (ev *eval[T]) compareName(op tokenKind, leftType reflect.Type, right *nameExpr) func(reflect.Value) bool {
	// A simple * matches any non-default value.
	if op == tokenHas && !right.isString && right.name == "*" {
		return func(v reflect.Value) bool {
			if !v.IsValid() {
				return false
			}
			return !v.IsZero()
		}
	}

	rval, err := ev.parseLiteral(right.pos, leftType, right.name)
	if err != nil {
		ev.msgs = append(ev.msgs, err.Error())
		return nil
	}

	if op == tokenMatchesRegexp || op == tokenNotMatchesRegexp {
		re, err := regexp.Compile(rval.String())
		if err != nil {
			ev.msgs = append(ev.msgs, fmt.Sprintf("%s: failed to parse regular expression %q: %v", right.pos, right.name, err))
			return nil
		}

		if op == tokenMatchesRegexp {
			return func(v reflect.Value) bool {
				if !v.IsValid() {
					return false
				}
				return re.MatchString(v.String())
			}
		} else {
			return func(v reflect.Value) bool {
				if !v.IsValid() {
					return false
				}
				return !re.MatchString(v.String())
			}
		}
	}

	// String equality comparisons are case-insensitive.
	// TODO: Unicode normalization.
	if leftType.Kind() == reflect.String && (op == tokenEquals || op == tokenNotEquals || op == tokenHas) {
		rsval := rval.String()

		// For some reason we match the empty string
		// even if the field is not valid.
		if (op == tokenEquals || op == tokenHas) && rsval == "" && right.isString {
			return func(v reflect.Value) bool {
				if !v.IsValid() {
					return true
				}
				return len(v.String()) == 0
			}
		}

		var fn func(v reflect.Value) bool
		if !right.isComposite && len(rsval) > 0 && rsval[0] == '*' {
			suffix := rsval[1:]
			fn = func(v reflect.Value) bool {
				if !v.IsValid() {
					return false
				}
				s := v.String()
				return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
			}
		} else if !right.isComposite && len(rsval) > 0 && rsval[len(rsval)-1] == '*' {
			prefix := rsval[:len(rsval)-1]
			fn = func(v reflect.Value) bool {
				if !v.IsValid() {
					return false
				}
				s := v.String()
				return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
			}
		} else if op == tokenHas {
			rsvalLowered := strings.ToLower(rsval)
			boundary := func(b byte) bool {
				return b < utf8.RuneSelf && !unicode.IsLetter(rune(b)) && !unicode.IsDigit(rune(b))
			}
			fn = func(v reflect.Value) bool {
				if !v.IsValid() {
					return false
				}
				vs := v.String()
				idx := strings.Index(strings.ToLower(vs), rsvalLowered)
				if idx < 0 {
					return false
				}
				if idx > 0 && !boundary(vs[idx-1]) {
					return false
				}
				if idx+len(rsvalLowered) < len(vs) && !boundary(vs[idx+len(rsvalLowered)]) {
					return false
				}
				return true
			}
		} else {
			fn = func(v reflect.Value) bool {
				if !v.IsValid() {
					return false
				}
				return strings.EqualFold(v.String(), rsval)
			}
		}

		if op == tokenNotEquals {
			return func(v reflect.Value) bool {
				return !fn(v)
			}
		}

		if op == tokenHas {
			return func(v reflect.Value) bool {
				if !v.IsValid() {
					return false
				}
				if fn(v) {
					return true
				}
				words := splitString(v.String())
				if len(words) > 1 {
					for _, word := range words {
						if fn(reflect.ValueOf(word)) {
							return true
						}
					}
				}
				return false
			}
		}

		return fn
	}

	var cmpFn func(v reflect.Value) int
	switch leftType.Kind() {
	case reflect.Bool:
		rbval := rval.Bool()
		cmpFn = func(v reflect.Value) int {
			if v.Bool() {
				if rbval {
					return 0
				}
				return +1
			} else {
				if rbval {
					return -1
				}
				return 0
			}
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		rival := rval.Int()
		cmpFn = func(v reflect.Value) int {
			return cmp.Compare(v.Int(), rival)
		}

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		ruval := rval.Uint()
		cmpFn = func(v reflect.Value) int {
			return cmp.Compare(v.Uint(), ruval)
		}

	case reflect.Float32, reflect.Float64:
		rfval := rval.Float()
		cmpFn = func(v reflect.Value) int {
			return cmp.Compare(v.Float(), rfval)
		}
		cmpOp := ev.compareOp(op, cmpFn)
		return func(v reflect.Value) bool {
			if !v.IsValid() {
				return false
			}
			// Comparisons with NaN are always false,
			// except that we treat NaN == NaN as true.
			if math.IsNaN(v.Float()) {
				return math.IsNaN(rfval)
			}
			return cmpOp(v)
		}

	case reflect.String:
		rsval := rval.String()
		cmpFn = func(v reflect.Value) int {
			return strings.Compare(v.String(), rsval)
		}

	case reflect.Interface:
		// This call to Convert won't panic.
		// rval comes from parseLiteral,
		// which verified that the conversion
		// is OK using CanConvert.
		rival := rval.Convert(leftType).Interface()
		rsval := fmt.Sprint(rival)
		cmpFn = func(v reflect.Value) int {
			// This comparison can't panic.
			// rival comes from parseLiteral,
			// which only returns comparable types.
			if v.Interface() == rival {
				return 0
			}
			// Compare as strings. Seems to be the best we can do.
			return strings.Compare(fmt.Sprint(v.Interface()), rsval)
		}

	case reflect.Struct:
		if leftType == reflect.TypeFor[time.Time]() {
			rtval := rval.Interface().(time.Time)
			cmpFn = func(v reflect.Value) int {
				return v.Interface().(time.Time).Compare(rtval)
			}
			break
		}

		fallthrough
	default:
		panic("can't happen")
	}

	return ev.compareOp(op, cmpFn)
}

// compareOp returns a function that takes a comparison function
// and returns a function that returns a bool reporting whether
// the comparison function returned a value that matches the operation.
func (ev *eval[T]) compareOp(op tokenKind, cmpFn func(reflect.Value) int) func(reflect.Value) bool {
	var cmpOp func(int) bool
	switch op {
	case tokenLessThanEquals:
		cmpOp = func(c int) bool { return c <= 0 }
	case tokenLessThan:
		cmpOp = func(c int) bool { return c < 0 }
	case tokenGreaterThanEquals:
		cmpOp = func(c int) bool { return c >= 0 }
	case tokenGreaterThan:
		cmpOp = func(c int) bool { return c > 0 }
	case tokenEquals, tokenHas:
		cmpOp = func(c int) bool { return c == 0 }
	case tokenNotEquals:
		cmpOp = func(c int) bool { return c != 0 }
	default:
		panic("can't happen")
	}

	return func(v reflect.Value) bool {
		if !v.IsValid() {
			return false
		}
		return cmpOp(cmpFn(v))
	}
}

// compareFunction returns a function that takes a reflect.Value
// of type leftType and returns the result of a comparison of that
// value with right, where right is a function call.
// This returns nil on error.
func (ev *eval[T]) compareFunction(op tokenKind, leftType reflect.Type, right *functionExpr) func(context.Context, reflect.Value) bool {
	ev.msgs = append(ev.msgs, "compareFunction not implemented")
	return func(context.Context, reflect.Value) bool { return false }
}

// multipleCompare returns a function that returns the result of a
// comparison, where the left hand side of the comparison contains
// multiple values.
func (ev *eval[T]) multipleCompare(e *comparisonExpr) func(context.Context, T) bool {
	switch el := e.left.(type) {
	case *nameExpr:
		left, leftType := ev.fieldValue(el)
		if left == nil {
			return ev.alwaysFalse()
		}
		return ev.multipleName(e.op, left, leftType, e.right)

	case *memberExpr:
		return ev.multipleMember(e.pos, e.op, el, e.right)

	default:
		panic("can't happen")
	}
}

// matchMethod is used to track which methods are called by match.
// Since match looks through all fields and methods of a type,
// it can wind up in an infinite recursion. matchMethod stops it.
// This is imperfect because it may be appropriate to call the same
// method multiple times on different values, but it's close enough,
// and an actual implementation can sidestep the loop through other methods.
type matchMethod struct {
	typ    reflect.Type
	method string
}

// match returns a function that takes a value of type typ
// and reports whether it is equal to, or contains, val.
// This returns nil if the types can't match.
func (ev *eval[T]) match(typ reflect.Type, e *nameExpr) func(context.Context, reflect.Value) bool {
	return ev.matchSeen(typ, e, make(map[matchMethod]bool))
}

// matchSeen is like match but takes a map of types/methods
// we have already seen, to avoid infinite recursion.
func (ev *eval[T]) matchSeen(typ reflect.Type, e *nameExpr, seen map[matchMethod]bool) func(context.Context, reflect.Value) bool {
	subFn := ev.matchValue(typ, e, seen)
	return ev.matchMethods(typ, e, subFn, seen)
}

// matchValue is like matches only on the value, ignoring methods.
func (ev *eval[T]) matchValue(typ reflect.Type, e *nameExpr, seen map[matchMethod]bool) func(context.Context, reflect.Value) bool {
	switch typ.Kind() {
	case reflect.Slice, reflect.Array:
		return ev.matchSlice(typ, e, seen)
	case reflect.Map:
		return ev.matchMap(typ, e, seen)
	case reflect.Struct:
		return ev.matchStruct(typ, e, seen)
	}

	eval, err := ev.parseLiteral(e.pos, typ, e.name)
	if err != nil {
		// Ignore the error, just don't match.
		return nil
	}

	switch typ.Kind() {
	case reflect.Bool:
		ebval := eval.Bool()
		return func(ctx context.Context, v reflect.Value) bool {
			return v.Bool() == ebval
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		eival := eval.Int()
		return func(ctx context.Context, v reflect.Value) bool {
			return v.Int() == eival
		}

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		euval := eval.Uint()
		return func(ctx context.Context, v reflect.Value) bool {
			return v.Uint() == euval
		}

	case reflect.Float32, reflect.Float64:
		efval := eval.Float()
		return func(ctx context.Context, v reflect.Value) bool {
			return v.Float() == efval
		}

	case reflect.String:
		// For strings we look for a case-insentive substring match.
		esval := strings.ToLower(eval.String())
		return func(ctx context.Context, v reflect.Value) bool {
			return strings.Contains(strings.ToLower(v.String()), esval)
		}

	case reflect.Interface:
		// This call to Convert won't panic.
		// eval comes from parseLiteral,
		// which verified that the conversion
		// is OK using CanConvert.
		eival := eval.Convert(typ).Interface()
		esval := fmt.Sprint(eival)
		return func(ctx context.Context, v reflect.Value) bool {
			// This comparison can't panic.
			// rival comes from parseLiteral,
			// which only returns comparable types.
			if v.Interface() == eival {
				return true
			}
			// Compare as strings. Seems to be the best we can do.
			return fmt.Sprint(v.Interface()) == esval
		}

	default:
		panic("can't happen")
	}
}

// matchSlice is match against all elements of a slice or array.
// It returns a function that takes a value of type typ
// and reports whether it contains a value val.
// This returns nil if the types can't match.
func (ev *eval[T]) matchSlice(typ reflect.Type, e *nameExpr, seen map[matchMethod]bool) func(context.Context, reflect.Value) bool {
	elementMatch := ev.matchSeen(typ.Elem(), e, seen)
	if elementMatch == nil {
		return nil
	}

	return func(ctx context.Context, v reflect.Value) bool {
		for i := range v.Len() {
			if elementMatch(ctx, v.Index(i)) {
				return true
			}
		}
		return false
	}
}

// matchMap is match against all elements of a map.
// It returns a function that takes a value of type typ
// and reports whether it contains a value e.
// This returns nil if the types can't match.
func (ev *eval[T]) matchMap(typ reflect.Type, e *nameExpr, seen map[matchMethod]bool) func(context.Context, reflect.Value) bool {
	elementMatch := ev.matchSeen(typ.Elem(), e, seen)
	if elementMatch == nil {
		return nil
	}

	return func(ctx context.Context, v reflect.Value) bool {
		mi := v.MapRange()
		for mi.Next() {
			if elementMatch(ctx, mi.Value()) {
				return true
			}
		}
		return false
	}
}

// matchStruct is match against all elements of a struct.
// It returns a value that takes a value of type typ
// and reports whether it contains a value val.
// This returns nil if the types can't match.
func (ev *eval[T]) matchStruct(typ reflect.Type, e *nameExpr, seen map[matchMethod]bool) func(context.Context, reflect.Value) bool {
	return ev.compareStructFields(typ,
		func(ftype reflect.Type) func(context.Context, reflect.Value) bool {
			return ev.matchSeen(ftype, e, seen)
		},
	)
}

// matchMethods handles methods for match.
// It returns a function that calls methods on typ to match against e.
// If subFn is not nil it is called first for a match.
// This returns nil if subFn is nil and the methods can't match.
func (ev *eval[T]) matchMethods(typ reflect.Type, e *nameExpr, subFn func(context.Context, reflect.Value) bool, seen map[matchMethod]bool) func(context.Context, reflect.Value) bool {
	if typ.NumMethod() == 0 {
		return subFn
	}

	var methodCalls []func(context.Context, reflect.Value) reflect.Value
	var matchFns []func(context.Context, reflect.Value) bool
	for i := range typ.NumMethod() {
		m := typ.Method(i)
		if !methodTypeOK(typ, m) {
			continue
		}

		key := matchMethod{
			typ:    typ,
			method: m.Name,
		}
		if seen[key] {
			continue
		}
		seen[key] = true

		matchFn := ev.matchSeen(m.Type.Out(0), e, seen)
		if matchFn != nil {
			methodCalls = append(methodCalls, callMethod(typ, m))
			matchFns = append(matchFns, matchFn)
		}
	}
	if len(methodCalls) == 0 {
		return subFn
	}

	return func(ctx context.Context, v reflect.Value) bool {
		if subFn != nil && subFn(ctx, v) {
			return true
		}
		for i := range methodCalls {
			v := methodCalls[i](ctx, v)
			if !v.IsValid() {
				// Method returned an error: no match.
				continue
			}
			if matchFns[i](ctx, v) {
				return true
			}
		}
		return false
	}
}

// parseLiteral parses a literal string as a reflect.Type,
// returning a reflect.Value.
// The warn parameter tells the function whether to add a message
// to ev.msgs if the string can't be parsed for the type.
// The err result is nil if the parse succeeded.
func (ev *eval[T]) parseLiteral(pos position, typ reflect.Type, val string) (reflect.Value, error) {
	badParse := func(err error) (reflect.Value, error) {
		err = fmt.Errorf("%s: can't parse %q as type %s: %v", pos, val, typ, err)
		return reflect.Value{}, err
	}

	var rval reflect.Value
	switch typ.Kind() {
	case reflect.Bool:
		bval, err := strconv.ParseBool(strings.ToLower(val))
		if err != nil {
			return badParse(err)
		}
		rval = reflect.ValueOf(bval)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		ival, err := strconv.ParseInt(val, 0, 64)
		if err != nil {
			if (strings.HasSuffix(val, "s") || strings.HasSuffix(val, "S")) && typ.Kind() == reflect.Int64 {
				// Try parsing this as a duration.
				d, err := time.ParseDuration(strings.ToLower(val))
				if err != nil {
					return badParse(err)
				}

				rval = reflect.ValueOf(d)
				break
			}

			return badParse(err)
		}
		rval = reflect.ValueOf(ival)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		uval, err := strconv.ParseUint(val, 0, 64)
		if err != nil {
			return badParse(err)
		}
		rval = reflect.ValueOf(uval)

	case reflect.Float32, reflect.Float64:
		fval, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return badParse(err)
		}
		rval = reflect.ValueOf(fval)

	case reflect.String:
		rval = reflect.ValueOf(val)

	case reflect.Interface:
		// We support this case for map keys when called from
		// fieldAccessor.
		rval = reflect.ValueOf(val)
		if !rval.CanConvert(typ) {
			return badParse(errors.New("interface conversion failed"))
		}

	case reflect.Struct:
		if typ == reflect.TypeFor[time.Time]() {
			tval, err := parseTime(val)
			if err != nil {
				return badParse(err)
			}
			rval = reflect.ValueOf(tval)
			break
		}

		fallthrough
	default:
		err := fmt.Errorf("%s: unsupported literal type %s (val %q)", pos, typ, val)
		return reflect.Value{}, err
	}

	frval := rval.Convert(typ)
	if !frval.IsValid() {
		err := fmt.Errorf("%s: failed to convert literal %q type %s to type %s", pos, val, rval.Type(), typ)
		return reflect.Value{}, err
	}
	return frval, nil
}

// has returns a function that returns whether a left value contains
// a right value.
func (ev *eval[T]) has(e *comparisonExpr) func(context.Context, T) bool {
	switch el := e.left.(type) {
	case *nameExpr:
		return ev.hasName(el, e.right)

	case *memberExpr:
		return ev.multipleMember(e.pos, tokenHas, el, e.right)

	case *binaryExpr, *unaryExpr, *comparisonExpr, *functionExpr:
		ev.msgs = append(ev.msgs, "invalid expression on left of has operator")
		return ev.alwaysFalse()

	default:
		panic("can't happen")
	}
}

// hasName returns a function that returns whether the name el
// has the value in er.
func (ev *eval[T]) hasName(el *nameExpr, er Expr) func(context.Context, T) bool {
	left, leftType := ev.fieldValue(el)
	if left == nil {
		return ev.alwaysFalse()
	}

	switch leftType.Kind() {
	case reflect.Map:
		// This reports whether the map contains a key that matches er.

		if ern, ok := er.(*nameExpr); ok && !ern.isString && !strings.Contains(ern.name, "*") && leftType.Key().Kind() == reflect.String {
			// This is m:foo where the map keys are strings.
			// We can just do a lookup.
			rval := reflect.ValueOf(ern.name).Convert(leftType.Key())
			return func(ctx context.Context, v T) bool {
				mval := left(ctx, v)
				if !mval.IsValid() {
					return false
				}
				return mval.MapIndex(rval).IsValid()
			}
		}

		return ev.multipleName(tokenHas, left, leftType, er)

	case reflect.Slice, reflect.Array:
		// This reports whether the slice contains an element
		// that matches er.

		return ev.multipleName(tokenHas, left, leftType, er)

	case reflect.Struct:
		if leftType == reflect.TypeFor[time.Time]() {
			cmpFn := ev.compareVal(tokenHas, leftType, er)
			if cmpFn == nil {
				return ev.alwaysFalse()
			}
			return func(ctx context.Context, v T) bool {
				return cmpFn(ctx, left(ctx, v))
			}
		}

		// This reports whether the struct has a member
		// that matches er. We don't need an iter.Seq for this case.

		cmpFn := ev.compareStructFields(leftType,
			func(ftype reflect.Type) func(context.Context, reflect.Value) bool {
				return ev.compareVal(tokenHas, ftype, er)
			},
		)
		if cmpFn == nil {
			return ev.alwaysFalse()
		}
		return func(ctx context.Context, v T) bool {
			val := left(ctx, v)
			if !val.IsValid() {
				return false
			}
			return cmpFn(ctx, val)
		}

	default:
		// Some sort of scalar value.

		cmpFn := ev.compareVal(tokenHas, leftType, er)
		if cmpFn == nil {
			return ev.alwaysFalse()
		}

		return func(ctx context.Context, v T) bool {
			return cmpFn(ctx, left(ctx, v))
		}
	}
}

// multipleVal returns a function that reports whether the value
// left, of type leftType, matches the value in er.
// leftType is expected to have multiple values.
func (ev *eval[T]) multipleName(op tokenKind, left func(context.Context, T) reflect.Value, leftType reflect.Type, er Expr) func(context.Context, T) bool {
	cmpFn := ev.multipleCompareVal(op, leftType.Elem(), er)
	if cmpFn == nil {
		return ev.alwaysFalse()
	}

	switch leftType.Kind() {
	case reflect.Map:
		return func(ctx context.Context, v T) bool {
			mval := left(ctx, v)
			if !mval.IsValid() {
				return false
			}
			seq := func(yield func(reflect.Value) bool) {
				mi := mval.MapRange()
				for mi.Next() {
					if !yield(mi.Value()) {
						return
					}
				}
			}
			return cmpFn(ctx, seq)
		}

	case reflect.Slice, reflect.Array:
		return func(ctx context.Context, v T) bool {
			sval := left(ctx, v)
			if !sval.IsValid() {
				return false
			}
			seq := func(yield func(reflect.Value) bool) {
				for i := range sval.Len() {
					if !yield(sval.Index(i)) {
						return
					}
				}
			}
			return cmpFn(ctx, seq)
		}

	default:
		panic("can't happen")
	}
}

// holderEmptyStringType tells multipleMemberHolder whether we are
// comparing against an empty string.
type holderEmptyStringType bool

const (
	holderNonEmptyString holderEmptyStringType = false
	holderEmptyString    holderEmptyStringType = true
)

// multipleMember returns a function that returns whether the member el
// matches the value in er.
func (ev *eval[T]) multipleMember(pos position, op tokenKind, el *memberExpr, er Expr) func(context.Context, T) bool {
	emptyString := holderNonEmptyString
	if erName, ok := er.(*nameExpr); ok && erName.isString && erName.name == "" {
		emptyString = holderEmptyString
	}

	hfn, htype := ev.multipleMemberHolder(el, emptyString)
	if hfn == nil {
		return ev.alwaysFalse()
	}

	var cmpFn func(context.Context, iter.Seq[reflect.Value]) bool
	var seqFn func(iter.Seq[reflect.Value]) iter.Seq[reflect.Value]

	switch htype.Kind() {
	case reflect.Map:
		cmpFn = ev.multipleCompareVal(op, htype.Elem(), er)
		if cmpFn == nil {
			return ev.alwaysFalse()
		}

		seqFn = func(seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
			return func(yield func(reflect.Value) bool) {
				for v := range seq {
					mi := v.MapRange()
					for mi.Next() {
						if !yield(mi.Value()) {
							return
						}
					}
				}
			}
		}

	case reflect.Slice, reflect.Array:
		cmpFn = ev.multipleCompareVal(op, htype.Elem(), er)
		if cmpFn == nil {
			return ev.alwaysFalse()
		}

		seqFn = func(seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
			return func(yield func(reflect.Value) bool) {
				for v := range seq {
					for i := range v.Len() {
						if !yield(v.Index(i)) {
							return
						}
					}
				}
			}
		}

	case reflect.Struct:
		cmpStructFn := ev.compareStructFields(htype,
			func(ftype reflect.Type) func(context.Context, reflect.Value) bool {
				return ev.compareVal(op, ftype, er)
			},
		)
		if cmpStructFn == nil {
			return ev.alwaysFalse()
		}

		cmpFn = func(ctx context.Context, seq iter.Seq[reflect.Value]) bool {
			for v := range seq {
				if cmpStructFn(ctx, v) {
					return true
				}
			}
			return false
		}

		seqFn = func(seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
			return seq
		}

	default:
		cmpFn = ev.multipleCompareVal(op, htype, er)
		if cmpFn == nil {
			return ev.alwaysFalse()
		}

		seqFn = func(seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
			return seq
		}
	}

	return func(ctx context.Context, v T) bool {
		input := func(yield func(reflect.Value) bool) {
			yield(reflect.ValueOf(v))
		}
		return cmpFn(ctx, seqFn(hfn(ctx, input)))
	}
}

// multipleMemberHolder is called when we are comparing against
// an aggregate A, and we are fetching a field F from that aggregate.
// If A is a map then F is a key in the map.
// If A is a slice then F is checked against each element of the slice.
// If A is a struct then F is a field of the struct.
// F is not passed to this function, it's in the caller.
// To make this work for all cases, we return a function that
// takes the value A and returns a sequence of reflect.Value's.
// We also return the type of those reflect.Value's.
// This returns nil, nil on failure.
func (ev *eval[T]) multipleMemberHolder(e *memberExpr, emptyString holderEmptyStringType) (func(context.Context, iter.Seq[reflect.Value]) iter.Seq[reflect.Value], reflect.Type) {
	var hfn func(context.Context, iter.Seq[reflect.Value]) iter.Seq[reflect.Value]
	var htype reflect.Type

	switch holder := e.holder.(type) {
	case *memberExpr:
		hfn, htype = ev.multipleMemberHolder(holder, emptyString)
		if hfn == nil {
			return nil, nil
		}

	case *nameExpr:
		// This should be the name of a field or method in T.
		ffn, ftype := ev.fieldAccessor(holder.pos, reflect.TypeFor[T](), holder.name)
		if ffn == nil {
			return nil, nil
		}
		hfn = func(ctx context.Context, seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
			return func(yield func(reflect.Value) bool) {
				for v := range seq {
					rv := ffn(ctx, v)
					if rv.IsValid() {
						if !yield(rv) {
							return
						}
					}
				}
			}
		}
		htype = ftype
	}

	// Now apply e.member to hfn and htype.

	switch htype.Kind() {
	case reflect.Map:
		// For some reason when comparing against an empty string
		// we match even if the key doesn't match.
		if emptyString == holderEmptyString && htype.Key().Kind() == reflect.String {
			rhfn := func(ctx context.Context, seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
				return func(yield func(reflect.Value) bool) {
					yield(reflect.ValueOf(""))
				}
			}
			return rhfn, htype.Elem()
		}

		kval, err := ev.parseLiteral(e.pos, htype.Key(), e.member)
		if err != nil {
			ev.msgs = append(ev.msgs, err.Error())
			return nil, nil
		}

		rhfn := func(ctx context.Context, seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
			return func(yield func(reflect.Value) bool) {
				for v := range hfn(ctx, seq) {
					rv := v.MapIndex(kval)
					if rv.IsValid() {
						if !yield(rv) {
							return
						}
					}
				}
			}
		}
		return rhfn, htype.Elem()

	case reflect.Slice, reflect.Array:
		ffn, ftype := ev.fieldAccessor(e.pos, htype.Elem(), e.member)
		if ffn == nil {
			return nil, nil
		}

		rhfn := func(ctx context.Context, seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
			return func(yield func(reflect.Value) bool) {
				for v := range hfn(ctx, seq) {
					for i := range v.Len() {
						rv := ffn(ctx, v.Index(i))
						if rv.IsValid() {
							if !yield(rv) {
								return
							}
						}
					}
				}
			}
		}
		return rhfn, ftype

	default:
		ffn, ftype := ev.fieldAccessor(e.pos, htype, e.member)
		if ffn == nil {
			return nil, nil
		}

		rhfn := func(ctx context.Context, seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
			return func(yield func(reflect.Value) bool) {
				for v := range hfn(ctx, seq) {
					rv := ffn(ctx, v)
					if rv.IsValid() {
						if !yield(rv) {
							return
						}
					}
				}
			}
		}
		return rhfn, ftype
	}
}

// multipleCompareVal returns a function that takes a sequence of reflect.Value,
// where each element of the sequence has type leftType,
// and evaluates that sequence with the parameter right.
// The function may evaluate the sequence multiple times.
// The right value may be a logical expression with AND and OR.
// This returns nil on error.
func (ev *eval[T]) multipleCompareVal(op tokenKind, leftType reflect.Type, right Expr) func(context.Context, iter.Seq[reflect.Value]) bool {
	switch right := right.(type) {
	case *binaryExpr:
		lf := ev.multipleCompareVal(op, leftType, right.left)
		rf := ev.multipleCompareVal(op, leftType, right.right)
		if lf == nil || rf == nil {
			return nil
		}
		switch right.op {
		case tokenAnd:
			return func(ctx context.Context, seq iter.Seq[reflect.Value]) bool {
				return lf(ctx, seq) && rf(ctx, seq)
			}
		case tokenOr:
			return func(ctx context.Context, seq iter.Seq[reflect.Value]) bool {
				return lf(ctx, seq) || rf(ctx, seq)
			}
		default:
			panic("can't happen")
		}

	case *unaryExpr:
		uf := ev.multipleCompareVal(op, leftType, right.expr)
		if uf == nil {
			return nil
		}
		switch right.op {
		case tokenMinus, tokenNot:
			return func(ctx context.Context, seq iter.Seq[reflect.Value]) bool {
				return !uf(ctx, seq)
			}
		default:
			panic("can't happen")
		}

	default:
		cmpFn := ev.compareVal(op, leftType, right)
		if cmpFn == nil {
			return nil
		}
		return func(ctx context.Context, seq iter.Seq[reflect.Value]) bool {
			for v := range seq {
				if cmpFn(ctx, v) {
					return true
				}
			}
			return false
		}
	}
}

// compareStructFields takes a struct type and a function that
// builds match functions. The match function builder will differ
// based on what the caller is doing, but will in general compare
// some value against a field of the struct. The match function
// builder will returns nil if no match is possible, because the
// types are incomparable.
//
// compareStructFields returns a function that takes a reflect.Value
// of the structtype and returns whether any of the match functions
// match that value. It returns nil if none of the fields match.
func (ev *eval[T]) compareStructFields(typ reflect.Type, buildMatchFn func(reflect.Type) func(context.Context, reflect.Value) bool) func(context.Context, reflect.Value) bool {
	var fieldIndexes [][]int
	var matchFns []func(context.Context, reflect.Value) bool
	for i := range typ.NumField() {
		field := typ.Field(i)

		// Ignore unexported fields. We are matching on types
		// that are defined by a program that expects them
		// to be matched against, so any field that should
		// support matching can either be exported,
		// or can be returned by an exported method.
		if !field.IsExported() {
			continue
		}

		matchFn := buildMatchFn(field.Type)
		if matchFn != nil {
			fieldIndexes = append(fieldIndexes, field.Index)
			matchFns = append(matchFns, matchFn)
		}
	}
	if len(fieldIndexes) == 0 {
		return nil
	}

	return func(ctx context.Context, v reflect.Value) bool {
		for i := range fieldIndexes {
			f, err := v.FieldByIndexErr(fieldIndexes[i])
			if err == nil && matchFns[i](ctx, f) {
				return true
			}
		}
		return false
	}
}

// fieldName looks for a field by name in a type. We permit matching
// both on the field name and on the JSON field name if any.
func fieldName(typ reflect.Type, field string) (reflect.StructField, bool) {
	if typ.Kind() != reflect.Struct {
		return reflect.StructField{}, false
	}

	if sf, ok := typ.FieldByName(field); ok {
		return sf, ok
	}

	for _, sf := range reflect.VisibleFields(typ) {
		if camelCaseMatch(field, sf.Name) {
			return sf, true
		}

		jsonTag, ok := sf.Tag.Lookup("json")
		if ok && jsonTag != "-" {
			tagName, _, _ := strings.Cut(jsonTag, ",")
			if tagName == field || camelCaseMatch(tagName, field) {
				return sf, true
			}
		}
	}

	return reflect.StructField{}, false
}

// methodName looks for a method by name in a type.
// The method must take no arguments,
// and must return either a single value
// or two values with the second one being type error.
func methodName(typ reflect.Type, method string) (reflect.Method, bool) {
	if m, ok := typ.MethodByName(method); ok && methodTypeOK(typ, m) {
		return m, true
	}

	for i := range typ.NumMethod() {
		m := typ.Method(i)
		if camelCaseMatch(method, m.Name) && methodTypeOK(typ, m) {
			return m, true
		}
	}

	return reflect.Method{}, false
}

// methodTypeOK reports whether the method m defined on type typ
// could be called by the evaluator.
func methodTypeOK(typ reflect.Type, m reflect.Method) bool {
	// Ignore unexported methods. We are matching on types
	// that are defined by a program that expects them
	// to be matched against, so any method that should
	// support matching can be exported one way or another.
	if !m.IsExported() {
		return false
	}

	// Ignore String and GoString methods. They are misleading
	// because they will match non-string values against string arguments.
	if m.Name == "String" || m.Name == "GoString" {
		return false
	}

	wantIn := 0
	if typ.Kind() != reflect.Interface {
		// The receiver is the first argument,
		// so numIn == 1 means no regular arguments.
		wantIn = 1
	}
	switch m.Type.NumIn() {
	case wantIn:
	case wantIn + 1:
		// This is OK if the argument is a context.Context.
		if m.Type.In(wantIn) != reflect.TypeFor[context.Context]() {
			return false
		}
	default:
		return false
	}

	switch m.Type.NumOut() {
	case 1:
		return true
	case 2:
		return m.Type.Out(1) == reflect.TypeFor[error]()
	default:
		return false
	}
}

// callMethod returns a function that calls method m on its argument,
// which is of type typ, returning a reflect.Value.
// If the argument is an invalid Value, the function returns it.
// If the method call fails, the function returns an invalid reflect.Value.
// callMethod handles passing a Context if one is expected.
func callMethod(typ reflect.Type, m reflect.Method) func(context.Context, reflect.Value) reflect.Value {
	// See if we should pass a context.Context.
	wantIn := 0
	if typ.Kind() != reflect.Interface {
		// The receiver is the first argument,
		// so numIn == 1 means no regular arguments.
		wantIn = 1
	}

	index := m.Index
	var call func(context.Context, reflect.Value) []reflect.Value
	switch m.Type.NumIn() {
	case wantIn:
		call = func(ctx context.Context, v reflect.Value) []reflect.Value {
			return v.Method(index).Call(nil)
		}
	case wantIn + 1:
		call = func(ctx context.Context, v reflect.Value) []reflect.Value {
			args := []reflect.Value{
				reflect.ValueOf(ctx),
			}
			return v.Method(index).Call(args)
		}
	default:
		panic("can't happen")
	}

	// Handle any error result.
	switch m.Type.NumOut() {
	case 1:
		return func(ctx context.Context, v reflect.Value) reflect.Value {
			if !v.IsValid() {
				return v
			}
			return call(ctx, v)[0]
		}
	case 2:
		return func(ctx context.Context, v reflect.Value) reflect.Value {
			if !v.IsValid() {
				return v
			}
			retVals := call(ctx, v)
			if !retVals[1].IsNil() {
				// We got an error; ignore the value.
				return reflect.Value{}
			}
			return retVals[0]
		}
	default:
		panic("can't happen")
	}
}

// camelCaseMatch reports whether two strings match using either camel
// case or snake case. That is, "field_name" matches "fieldName"
// or "fieldname". We do a case-insensitive match of the first letter
// and of each letter following an underscore.
func camelCaseMatch(a, b string) bool {
	first := true
	for a != "" {
		if b == "" {
			return false
		}

		ac, asize := utf8.DecodeRuneInString(a)
		a = a[asize:]
		bc, bsize := utf8.DecodeRuneInString(b)
		b = b[bsize:]

		if ac == bc {
			continue
		}

		if first {
			first = false
			if unicode.ToUpper(ac) == unicode.ToUpper(bc) {
				continue
			}
		}

		if ac == '_' && len(a) > 0 {
			ac, asize = utf8.DecodeRuneInString(a)
			a = a[asize:]
			ac = unicode.ToUpper(ac)
		}

		if bc == '_' && len(b) > 0 {
			bc, bsize = utf8.DecodeRuneInString(b)
			b = b[bsize:]
			bc = unicode.ToUpper(bc)
		}

		if ac != bc {
			return false
		}
	}

	return b == ""
}

// splitString splits a string into a series of words to match.
func splitString(s string) []string {
	var sb strings.Builder
	var ret []string
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			// Whitespace between words.
			if sb.Len() > 0 {
				ret = append(ret, sb.String())
				sb.Reset()
			}
		case isTextStart(r):
			// A rune that does not stop a word.
			sb.WriteRune(r)
		default:
			// A rune that does stop a word,
			// and becomes a word by itself.
			if sb.Len() > 0 {
				ret = append(ret, sb.String())
				sb.Reset()
			}
			ret = append(ret, string(r))
		}
	}
	if sb.Len() > 0 {
		ret = append(ret, sb.String())
	}
	return ret
}

// timeFormats is a list of time formats we try for parsing
// if RFC3339 fails.
var timeFormats = []string{
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02T15",
	time.DateOnly,
	"2006-01",
	"2006",
	"January _2 2006",
	"January 2006",
	"Jan _2 2006",
	"Jan 2006",
}

// parseTime parses the kinds of timestamps we expect.
func parseTime(s string) (time.Time, error) {
	tval, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return tval, nil
	}
	for _, tf := range timeFormats {
		tval, nextErr := time.Parse(tf, s)
		if nextErr == nil {
			return tval, nil
		}
	}
	// Return the 3339 error if we can't parse the string.
	return time.Time{}, err
}

// alwaysFalse is an evaluator function that always returns false.
func (ev *eval[T]) alwaysFalse() func(context.Context, T) bool {
	return func(context.Context, T) bool {
		return false
	}
}
