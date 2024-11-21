// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter

import (
	"cmp"
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
func Evaluator[T any](e Expr, functions Functions) (func(T) bool, []string) {
	ev := eval[T]{
		functions: functions,
	}

	if e == nil {
		fn := func(T) bool {
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
func (ev *eval[T]) expr(e Expr) func(T) bool {
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
func (ev *eval[T]) binary(e *binaryExpr) func(T) bool {
	left := ev.expr(e.left)
	right := ev.expr(e.right)
	switch e.op {
	case tokenAnd:
		return func(v T) bool {
			return left(v) && right(v)
		}
	case tokenOr:
		return func(v T) bool {
			return left(v) || right(v)
		}
	default:
		panic("can't happen")
	}
}

// unary returns an evaluator function for a [unaryExpr].
func (ev *eval[T]) unary(e *unaryExpr) func(T) bool {
	sub := ev.expr(e.expr)
	switch e.op {
	case tokenMinus, tokenNot:
		return func(v T) bool {
			return !sub(v)
		}
	default:
		panic("can't happen")
	}
}

// comparison returns an evaluator function for a [comparisonExpr].
func (ev *eval[T]) comparison(e *comparisonExpr) func(T) bool {
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
				return func(v T) bool {
					return !fn(v)
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
func (ev *eval[T]) name(e *nameExpr) func(T) bool {
	// A literal can be matched anywhere.
	typ := reflect.TypeFor[T]()
	if typ.NumField() == 0 {
		// Return a function that matches e against the value.
		matchFn := ev.match(typ, e)
		if matchFn == nil {
			ev.msgs = append(ev.msgs, fmt.Sprintf("%s: value of type %s can't match %q", e.pos, typ, e.name))
			return ev.alwaysFalse()
		}

		return func(v T) bool {
			return matchFn(reflect.ValueOf(v))
		}
	} else {
		cmpFn := ev.compareStructFields(typ,
			func(ftype reflect.Type) func(reflect.Value) bool {
				return ev.match(ftype, e)
			},
		)
		if cmpFn == nil {
			ev.msgs = append(ev.msgs, fmt.Sprintf("%s: no fields of value %s match %q", e.pos, typ, e.name))
			return ev.alwaysFalse()
		}

		return func(v T) bool {
			return cmpFn(reflect.ValueOf(v))
		}
	}
}

// member returns an evaluator function for a [memberExpr].
// This is something like plain "a.b", not in a comparison.
// We accept a boolean value.
// It's not clear that AIP 160 permits this case.
func (ev *eval[T]) member(e *memberExpr) func(T) bool {
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
	return func(v T) bool {
		hval := valFn(v)
		if !hval.IsValid() {
			return false
		}
		fval := ffn(hval)
		if !fval.IsValid() {
			return false
		}
		return fval.Bool()
	}
}

// function returns an evaluator function for a [functionExpr].
func (ev *eval[T]) function(*functionExpr) func(T) bool {
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
		if htype.Kind() == reflect.Struct {
			sf, ok := fieldName(htype, e.member)
			if ok {
				if isMultipleType(sf.Type) {
					return true, nil
				}
				return false, sf.Type
			}
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
func (ev *eval[T]) fieldValue(e Expr) (func(T) reflect.Value, reflect.Type) {
	switch e := e.(type) {
	case *nameExpr:
		// This should be the name of a field in T.
		// We also permit a function with no extra arguments;
		// this can be used to resolve arbitrary names.
		if _, ok := fieldName(reflect.TypeFor[T](), e.name); ok {
			return ev.topFieldValue(e.pos, e.name)
		}
		if cfn, ok := ev.functions[e.name]; ok {
			return ev.fieldFunction(cfn)
		}
		if e.isString {
			fn := func(T) reflect.Value {
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
func (ev *eval[T]) fieldFunction(fn any) (func(T) reflect.Value, reflect.Type) {
	ev.msgs = append(ev.msgs, "fieldFunction not implemented")
	return func(T) reflect.Value { return reflect.Value{} }, reflect.TypeFor[int]()
}

// functionValue is called when a function expression appears on
// the left hand side of a comparison operation.
// It returns a function that evaluates to the value of the field,
// and also returns the type of the field.
// This returns nil, nil on failure.
func (ev *eval[T]) functionValue(e *functionExpr) (func(T) reflect.Value, reflect.Type) {
	ev.msgs = append(ev.msgs, "functionValue not implemented")
	return func(T) reflect.Value { return reflect.Value{} }, reflect.TypeFor[int]()
}

// memberValue returns a funtion that evaluates to a reflect.Value
// of a member expression.
// It also returns the reflect.Type of the value returned by the function.
// It returns nil, nil on failure.
func (ev *eval[T]) memberValue(e *memberExpr) (func(T) reflect.Value, reflect.Type) {
	valFn, vtyp := ev.memberHolder(e)
	if valFn == nil {
		return nil, nil
	}
	ffn, ftyp := ev.fieldAccessor(e.pos, vtyp, e.member)
	if ffn == nil {
		return nil, nil
	}
	fn := func(v T) reflect.Value {
		hval := valFn(v)
		if !hval.IsValid() {
			return hval
		}
		return ffn(hval)
	}
	return fn, ftyp
}

// memberHolder returns a function that evaluates to a reflect.Value
// of the holder part of a member expression, which is the expression
// that appears before the dot (in the expression s.f, the holder is s).
// This function also returns the reflect.Type of the value returned
// by the returned function.
// It returns nil, nil on failure.
func (ev *eval[T]) memberHolder(e *memberExpr) (func(T) reflect.Value, reflect.Type) {
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
func (ev *eval[T]) topFieldValue(pos position, name string) (func(T) reflect.Value, reflect.Type) {
	ffn, ftyp := ev.fieldAccessor(pos, reflect.TypeFor[T](), name)
	if ffn == nil {
		return nil, nil
	}
	fn := func(v T) reflect.Value {
		return ffn(reflect.ValueOf(v))
	}
	return fn, ftyp
}

// fieldAccessor returns a function that retrieves the value of
// the field name in the type holderType.
// The function takes a reflect.Value that is of type holderType,
// and returns a new reflect.Value.
// fieldAccessor also returns the type of the field.
// If there is no field, fieldAccessor returns nil, nil.
func (ev *eval[T]) fieldAccessor(pos position, holderType reflect.Type, name string) (func(reflect.Value) reflect.Value, reflect.Type) {
	switch holderType.Kind() {
	case reflect.Struct:
		sf, ok := fieldName(holderType, name)
		if !ok {
			ev.msgs = append(ev.msgs, fmt.Sprintf("%s: %s has no field %s", pos, holderType, name))
			return nil, nil
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
			return fn, ftype
		}
		pfn := func(v reflect.Value) reflect.Value {
			v = fn(v)
			if !v.IsValid() || v.IsNil() {
				return reflect.Value{}
			}
			return v.Elem()
		}
		return pfn, ftype.Elem()

	case reflect.Map:
		key, ok := ev.parseLiteral(pos, holderType.Key(), name, plWarnYes)
		if !ok {
			return nil, nil
		}

		fn := func(v reflect.Value) reflect.Value {
			if !v.IsValid() {
				return v
			}
			return v.MapIndex(key)
		}
		return fn, holderType.Elem()

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
			return fn, reflect.TypeFor[float64]()
		}

		ev.msgs = append(ev.msgs, fmt.Sprintf("%s: type %s has no fields (looking for field %s)", pos, holderType, name))
		return nil, nil
	}
}

// compare returns a function that returns the result of a
// comparison of a left value and a right value. The left value is
// a reflect.Value returned by left, and has type leftType.
// The right value is an expression, which may not be a field but may
// be a logical expression with AND and OR.
func (ev *eval[T]) compare(op tokenKind, left func(T) reflect.Value, leftType reflect.Type, right Expr) func(T) bool {
	cfn := ev.compareVal(op, leftType, right)
	if cfn == nil {
		return ev.alwaysFalse()
	}
	return func(v T) bool {
		return cfn(left(v))
	}
}

// compareVal returns a function that takes a reflect.Value of type leftType
// and returns the result of a comparison of that value with right.
// The right value is an expression, which may not be a field but may
// be a logical expression with AND and OR. This returns nil on error.
func (ev *eval[T]) compareVal(op tokenKind, leftType reflect.Type, right Expr) func(reflect.Value) bool {
	switch right := right.(type) {
	case *binaryExpr:
		lf := ev.compareVal(op, leftType, right.left)
		rf := ev.compareVal(op, leftType, right.right)
		if lf == nil || rf == nil {
			return nil
		}
		switch right.op {
		case tokenAnd:
			return func(v reflect.Value) bool {
				return lf(v) && rf(v)
			}
		case tokenOr:
			return func(v reflect.Value) bool {
				return lf(v) || rf(v)
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
			return func(v reflect.Value) bool {
				return !uf(v)
			}
		default:
			panic("can't happen")
		}

	case *nameExpr:
		return ev.compareName(op, leftType, right)

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
		return ev.compareName(op, leftType, ne)

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

	rval, ok := ev.parseLiteral(right.pos, leftType, right.name, plWarnYes)
	if !ok {
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
func (ev *eval[T]) compareFunction(op tokenKind, leftType reflect.Type, right *functionExpr) func(reflect.Value) bool {
	ev.msgs = append(ev.msgs, "compareFunction not implemented")
	return func(reflect.Value) bool { return false }
}

// multipleCompare returns a function that returns the result of a
// comparison, where the left hand side of the comparison contains
// multiple values.
func (ev *eval[T]) multipleCompare(e *comparisonExpr) func(T) bool {
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

// match returns a function that takes a value of type typ
// and reports whether it is equal to, or contains, val.
// This returns nil if the types can't match.
func (ev *eval[T]) match(typ reflect.Type, e *nameExpr) func(reflect.Value) bool {
	switch typ.Kind() {
	case reflect.Slice, reflect.Array:
		return ev.matchSlice(typ, e)
	case reflect.Map:
		return ev.matchMap(typ, e)
	case reflect.Struct:
		return ev.matchStruct(typ, e)
	}

	eval, ok := ev.parseLiteral(e.pos, typ, e.name, plWarnNo)
	if !ok {
		return nil
	}

	switch typ.Kind() {
	case reflect.Bool:
		ebval := eval.Bool()
		return func(v reflect.Value) bool {
			return v.Bool() == ebval
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		eival := eval.Int()
		return func(v reflect.Value) bool {
			return v.Int() == eival
		}

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		euval := eval.Uint()
		return func(v reflect.Value) bool {
			return v.Uint() == euval
		}

	case reflect.Float32, reflect.Float64:
		efval := eval.Float()
		return func(v reflect.Value) bool {
			return v.Float() == efval
		}

	case reflect.String:
		// For strings we look for a case-insentive substring match.
		esval := strings.ToLower(eval.String())
		return func(v reflect.Value) bool {
			return strings.Contains(strings.ToLower(v.String()), esval)
		}

	case reflect.Interface:
		// This call to Convert won't panic.
		// eval comes from parseLiteral,
		// which verified that the conversion
		// is OK using CanConvert.
		eival := eval.Convert(typ).Interface()
		esval := fmt.Sprint(eival)
		return func(v reflect.Value) bool {
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
func (ev *eval[T]) matchSlice(typ reflect.Type, e *nameExpr) func(reflect.Value) bool {
	elementMatch := ev.match(typ.Elem(), e)
	if elementMatch == nil {
		return nil
	}

	return func(v reflect.Value) bool {
		for i := range v.Len() {
			if elementMatch(v.Index(i)) {
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
func (ev *eval[T]) matchMap(typ reflect.Type, e *nameExpr) func(reflect.Value) bool {
	elementMatch := ev.match(typ.Elem(), e)
	if elementMatch == nil {
		return nil
	}

	return func(v reflect.Value) bool {
		mi := v.MapRange()
		for mi.Next() {
			if elementMatch(mi.Value()) {
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
func (ev *eval[T]) matchStruct(typ reflect.Type, e *nameExpr) func(reflect.Value) bool {
	return ev.compareStructFields(typ,
		func(ftype reflect.Type) func(reflect.Value) bool {
			return ev.match(ftype, e)
		},
	)
}

// plWarn is an argument to parseLiteral that tells it whether to
// report a warning for a literal that doesn't correspond to the type.
type plWarn bool

const (
	plWarnNo  plWarn = false
	plWarnYes plWarn = true
)

// parseLiteral parses a literal string as a reflect.Type,
// returning a reflect.Value.
// The warn parameter tells the function whether to add a message
// to ev.msgs if the string can't be parsed for the type.
// The bool result reports whether the parse succeeded.
func (ev *eval[T]) parseLiteral(pos position, typ reflect.Type, val string, warn plWarn) (reflect.Value, bool) {
	badParse := func(err error) (reflect.Value, bool) {
		if warn {
			ev.msgs = append(ev.msgs, fmt.Sprintf("%s: can't parse %q as type %s: %v", pos, val, typ, err))
		}
		return reflect.Value{}, false
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
		if warn {
			ev.msgs = append(ev.msgs, fmt.Sprintf("%s: unsupported literal type %s (val %q)", pos, typ, val))
		}
		return reflect.Value{}, false
	}

	frval := rval.Convert(typ)
	if !frval.IsValid() {
		if warn {
			ev.msgs = append(ev.msgs, fmt.Sprintf("%s: failed to convert literal %q type %s to type %s", pos, val, rval.Type(), typ))
		}
		return reflect.Value{}, false
	}
	return frval, true
}

// has returns a function that returns whether a left value contains
// a right value.
func (ev *eval[T]) has(e *comparisonExpr) func(T) bool {
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
func (ev *eval[T]) hasName(el *nameExpr, er Expr) func(T) bool {
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
			return func(v T) bool {
				mval := left(v)
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
			return func(v T) bool {
				return cmpFn(left(v))
			}
		}

		// This reports whether the struct has a member
		// that matches er. We don't need an iter.Seq for this case.

		cmpFn := ev.compareStructFields(leftType,
			func(ftype reflect.Type) func(reflect.Value) bool {
				return ev.compareVal(tokenHas, ftype, er)
			},
		)
		if cmpFn == nil {
			return ev.alwaysFalse()
		}
		return func(v T) bool {
			val := left(v)
			if !val.IsValid() {
				return false
			}
			return cmpFn(val)
		}

	default:
		// Some sort of scalar value.

		cmpFn := ev.compareVal(tokenHas, leftType, er)
		if cmpFn == nil {
			return ev.alwaysFalse()
		}

		return func(v T) bool {
			return cmpFn(left(v))
		}
	}
}

// multipleVal returns a function that reports whether the value
// left, of type leftType, matches the value in er.
// leftType is expected to have multiple values.
func (ev *eval[T]) multipleName(op tokenKind, left func(T) reflect.Value, leftType reflect.Type, er Expr) func(T) bool {
	cmpFn := ev.multipleCompareVal(op, leftType.Elem(), er)
	if cmpFn == nil {
		return ev.alwaysFalse()
	}

	switch leftType.Kind() {
	case reflect.Map:
		return func(v T) bool {
			mval := left(v)
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
			return cmpFn(seq)
		}

	case reflect.Slice, reflect.Array:
		return func(v T) bool {
			sval := left(v)
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
			return cmpFn(seq)
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
func (ev *eval[T]) multipleMember(pos position, op tokenKind, el *memberExpr, er Expr) func(T) bool {
	emptyString := holderNonEmptyString
	if erName, ok := er.(*nameExpr); ok && erName.isString && erName.name == "" {
		emptyString = holderEmptyString
	}

	hfn, htype := ev.multipleMemberHolder(el, emptyString)
	if hfn == nil {
		return ev.alwaysFalse()
	}

	var cmpFn func(iter.Seq[reflect.Value]) bool
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
			func(ftype reflect.Type) func(reflect.Value) bool {
				return ev.compareVal(op, ftype, er)
			},
		)
		if cmpStructFn == nil {
			return ev.alwaysFalse()
		}

		cmpFn = func(seq iter.Seq[reflect.Value]) bool {
			for v := range seq {
				if cmpStructFn(v) {
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

	return func(v T) bool {
		input := func(yield func(reflect.Value) bool) {
			yield(reflect.ValueOf(v))
		}
		return cmpFn(seqFn(hfn(input)))
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
func (ev *eval[T]) multipleMemberHolder(e *memberExpr, emptyString holderEmptyStringType) (func(iter.Seq[reflect.Value]) iter.Seq[reflect.Value], reflect.Type) {
	var hfn func(iter.Seq[reflect.Value]) iter.Seq[reflect.Value]
	var htype reflect.Type

	switch holder := e.holder.(type) {
	case *memberExpr:
		hfn, htype = ev.multipleMemberHolder(holder, emptyString)
		if hfn == nil {
			return nil, nil
		}

	case *nameExpr:
		// This should be the name of a field in T.
		ffn, ftype := ev.fieldAccessor(holder.pos, reflect.TypeFor[T](), holder.name)
		if ffn == nil {
			return nil, nil
		}
		hfn = func(seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
			return func(yield func(reflect.Value) bool) {
				for v := range seq {
					rv := ffn(v)
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
			rhfn := func(seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
				return func(yield func(reflect.Value) bool) {
					yield(reflect.ValueOf(""))
				}
			}
			return rhfn, htype.Elem()
		}

		kval, ok := ev.parseLiteral(e.pos, htype.Key(), e.member, plWarnYes)
		if !ok {
			return nil, nil
		}

		rhfn := func(seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
			return func(yield func(reflect.Value) bool) {
				for v := range hfn(seq) {
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

		rhfn := func(seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
			return func(yield func(reflect.Value) bool) {
				for v := range hfn(seq) {
					for i := range v.Len() {
						rv := ffn(v.Index(i))
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

		rhfn := func(seq iter.Seq[reflect.Value]) iter.Seq[reflect.Value] {
			return func(yield func(reflect.Value) bool) {
				for v := range hfn(seq) {
					rv := ffn(v)
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
func (ev *eval[T]) multipleCompareVal(op tokenKind, leftType reflect.Type, right Expr) func(iter.Seq[reflect.Value]) bool {
	switch right := right.(type) {
	case *binaryExpr:
		lf := ev.multipleCompareVal(op, leftType, right.left)
		rf := ev.multipleCompareVal(op, leftType, right.right)
		if lf == nil || rf == nil {
			return nil
		}
		switch right.op {
		case tokenAnd:
			return func(seq iter.Seq[reflect.Value]) bool {
				return lf(seq) && rf(seq)
			}
		case tokenOr:
			return func(seq iter.Seq[reflect.Value]) bool {
				return lf(seq) || rf(seq)
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
			return func(seq iter.Seq[reflect.Value]) bool {
				return !uf(seq)
			}
		default:
			panic("can't happen")
		}

	default:
		cmpFn := ev.compareVal(op, leftType, right)
		if cmpFn == nil {
			return nil
		}
		return func(seq iter.Seq[reflect.Value]) bool {
			for v := range seq {
				if cmpFn(v) {
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
func (ev *eval[T]) compareStructFields(typ reflect.Type, buildMatchFn func(reflect.Type) func(reflect.Value) bool) func(reflect.Value) bool {
	var fieldIndexes [][]int
	var matchFns []func(reflect.Value) bool
	for i := range typ.NumField() {
		field := typ.Field(i)
		matchFn := buildMatchFn(field.Type)
		if matchFn != nil {
			fieldIndexes = append(fieldIndexes, field.Index)
			matchFns = append(matchFns, matchFn)
		}
	}
	if len(fieldIndexes) == 0 {
		return nil
	}

	return func(v reflect.Value) bool {
		for i := range fieldIndexes {
			f, err := v.FieldByIndexErr(fieldIndexes[i])
			if err == nil && matchFns[i](f) {
				return true
			}
		}
		return false
	}
}

// fieldName looks for a field by name in a type. We permit matching
// both on the field name and on the JSON field name if any.
func fieldName(typ reflect.Type, field string) (reflect.StructField, bool) {
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

// camelCaseMatch reports whether two strings match using either camel
// case or snake case. That is, "field_name" matches "fieldName".
func camelCaseMatch(a, b string) bool {
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
func (ev *eval[T]) alwaysFalse() func(T) bool {
	return func(T) bool {
		return false
	}
}
