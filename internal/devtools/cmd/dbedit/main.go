// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Dbedit is an interactive editor for editing databases
// implementing [storage.DB].
//
// Usage:
//
//	dbedit database
//
// At the > prompt, the following commands are supported:
//
//	get(key [, end])
//	hex(key [, end])
//	list(start, end)
//	set(key, value)
//	delete(key [, end])
//	mvprefix(old, new)
//
// Get prints the value associated with the given key.
// If the end argument is given, get prints all key, value pairs
// with key k satisfying key ≤ k ≤ end.
//
// Hex is similar to get but prints hexadecimal dumps of
// the values instead of using value syntax.
//
// List lists all known keys k such that start ≤ k < end,
// but not their values.
//
// Set sets the value associated with the given key.
//
// Delete deletes the entry with the given key,
// printing an error if no such entry exists.
// If the end argument is given, delete deletes all entries
// with key k satisfying key ≤ k ≤ end.
//
// Mvprefix replaces every database entry with a key starting with old
// by an entry with a key starting with new instead (s/old/new/).
//
// Each of the key, value, start, and end arguments can be a
// Go quoted string or else a Go expression o(list) denoting an
// an [ordered code] value encoding the values in the argument list.
// The values in the list can be:
//
//   - a string value: a Go quoted string
//   - an ordered.Infinity value: the name Inf.
//   - an integer value: a possibly-signed integer literal
//   - a float64 value: a floating-point literal number (including a '.', 'e', or ,'p')
//   - a float64 value: float64(f) where f is an integer or floating-point literal
//     or NaN, Inf, +Inf, or -Inf
//   - a float32 value: float32(f) where f is an integer or floating-point literal
//     or NaN, Inf, +Inf, or -Inf
//   - rev(x) where x is one of the preceding choices, for a reverse-ordered value
//
// Note that Inf is an ordered infinity, while float64(Inf) is a floating-point infinity.
//
// The command output uses the same syntax to print keys and values.
//
// [ordered code]: https://pkg.go.dev/rsc.io/ordered
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"log"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"

	"golang.org/x/oscar/internal/gcp/firestore"
	"golang.org/x/oscar/internal/pebble"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/term"
	"rsc.io/ordered"
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: dbedit db\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("dbedit: ")
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 1 {
		usage()
	}

	dbspec := flag.Arg(0)
	db, err := openDB(dbspec)
	if err != nil {
		log.Fatal(err)
	}
	readonly := strings.HasPrefix(dbspec, "firestore:oscar-go-1,")

	if err := run(db, readonly); err != nil {
		log.Fatal(err)
	}
}

func run(db storage.DB, readonly bool) error {
	t := term.NewTerminal(os.Stdin, "dbedit> ")
	for {
		line, err := readLine(t)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		func() {
			if e := recover(); e != nil {
				fmt.Fprintf(os.Stderr, "?%s\n", e)
			}
			do(db, line, readonly)
		}()
	}
}

func do(db storage.DB, line string, readonly bool) {
	x, err := parser.ParseExpr(line)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse error: %v\n", err)
		return
	}

	call, ok := x.(*ast.CallExpr)
	if !ok {
		fmt.Fprintf(os.Stderr, "not a call expression\n")
		return
	}
	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		fmt.Fprintf(os.Stderr, "call of non-identifier\n")
		return
	}
	switch id.Name {
	default:
		fmt.Fprintf(os.Stderr, "unknown operation %s\n", id.Name)

	case "get", "hex", "list":
		key, end, ok := getRange(id.Name, call.Args, id.Name == "list")
		if !ok {
			return
		}
		if end == nil {
			val, ok := db.Get(key)
			if !ok {
				fmt.Fprintf(os.Stderr, "?missing key\n")
				return
			}
			if id.Name == "hex" {
				fmt.Printf("%s\n", hex.Dump(val))
				return
			}
			fmt.Printf("%s\n", decode(val))
			return
		}

		for key, valf := range db.Scan(key, end) {
			switch id.Name {
			case "get":
				fmt.Printf("%s: %s\n", decode(key), decode(valf()))
			case "hex":
				fmt.Printf("%s:\n%s\n", decode(key), hex.Dump(valf()))
			case "list":
				fmt.Printf("%s\n", decode(key))
			}
		}

	case "mvprefix":
		if readonly {
			fmt.Fprintf(os.Stderr, "cannot be called in readonly mode\n")
			return
		}
		if len(call.Args) != 2 {
			fmt.Fprintf(os.Stderr, "usage: mvprefix(old, new)\n")
			return
		}
		old, ok := getEnc(call.Args[0])
		if !ok {
			return
		}
		new, ok := getEnc(call.Args[1])
		if !ok {
			return
		}

		var last []byte
		for key, valf := range db.Scan(old, []byte("\xff")) {
			if !bytes.HasPrefix(key, old) {
				break
			}
			db.Set(append(new, key[len(old):]...), valf())
			last = bytes.Clone(key)
		}
		if last != nil {
			db.DeleteRange(old, last)
		}

	case "set":
		if readonly {
			fmt.Fprintf(os.Stderr, "cannot be called in readonly mode\n")
			return
		}
		if len(call.Args) != 2 {
			fmt.Fprintf(os.Stderr, "usage: set(key, value)\n")
			return
		}
		key, ok := getEnc(call.Args[0])
		if !ok {
			return
		}
		val, ok := getEnc(call.Args[1])
		if !ok {
			return
		}
		db.Set(key, val)

	case "delete":
		if readonly {
			fmt.Fprintf(os.Stderr, "cannot be called in readonly mode\n")
			return
		}
		key, end, ok := getRange(id.Name, call.Args, false)
		if !ok {
			return
		}
		if end == nil {
			db.Delete(key)
			return
		}
		db.DeleteRange(key, end)
	}
}

// getRange returns a pair of keys denoting a range in the database.
// name is used for error messages only.
// args is a list of one or two arguments describing the range.
// forceRange requires args to have length 2.
func getRange(name string, args []ast.Expr, forceRange bool) (lo, hi []byte, ok bool) {
	if forceRange && len(args) < 2 {
		fmt.Fprintf(os.Stderr, "need two arguments for key range in call to %s\n", name)
		return nil, nil, false
	}
	if len(args) > 2 {
		fmt.Fprintf(os.Stderr, "too many arguments in call to %s", name)
		return nil, nil, false
	}
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "no arguments in call to %s", name)
		return nil, nil, false
	}
	lo, ok = getEnc(args[0])
	if !ok {
		return nil, nil, false
	}
	if len(args) == 2 {
		hi, ok = getEnc(args[1])
		if !ok {
			return nil, nil, false
		}
	}
	return lo, hi, true
}

// getEnc encodes the expression as a []byte.
func getEnc(x ast.Expr) ([]byte, bool) {
	switch x := x.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			break
		}
		enc, err := strconv.Unquote(x.Value)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid quoted string %s\n", x.Value)
			return nil, false
		}
		return []byte(enc), true

	case *ast.CallExpr:
		fn, ok := x.Fun.(*ast.Ident)
		if !ok || fn.Name != "o" {
			break
		}
		var list []any
		for _, arg := range x.Args {
			a, ok := getArg(arg, 0)
			if !ok {
				return nil, false
			}
			list = append(list, a)
		}
		return ordered.Encode(list...), true
	}

	fmt.Fprintf(os.Stderr, "argument %s must be quoted string or o(list)\n", gofmt(x))
	return nil, false
}

const (
	noRev = 1 << iota
	forceFloat64
)

func getArg(x ast.Expr, flags int) (any, bool) {
	switch x := x.(type) {
	case *ast.BasicLit:
		switch x.Kind {
		case token.STRING:
			v, err := strconv.Unquote(x.Value)
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid quoted string %s\n", x.Value)
				return nil, false
			}
			return v, true
		case token.INT:
			if flags&forceFloat64 != 0 {
				f, err := strconv.ParseFloat(x.Value, 64)
				if err == nil {
					return f, true
				}
				break
			}
			i, err := strconv.ParseInt(x.Value, 0, 64)
			if err == nil {
				return i, true
			}
			u, err := strconv.ParseUint(x.Value, 0, 64)
			if err == nil {
				return u, true
			}

		case token.FLOAT:
			f, err := strconv.ParseFloat(x.Value, 64)
			if err == nil {
				return f, true
			}
		}

	case *ast.UnaryExpr:
		if x.Op == token.ADD || x.Op == token.SUB {
			sign := +1
			if x.Op == token.SUB {
				sign = -1
			}
			if id, ok := x.X.(*ast.Ident); ok && id.Name == "Inf" {
				if flags&forceFloat64 != 0 {
					return math.Inf(sign), true
				}
				fmt.Fprintf(os.Stderr, "must use float32(%s) or float64(%s)\n", gofmt(x), gofmt(x))
				return nil, false
			}
			if basic, ok := x.X.(*ast.BasicLit); ok {
				v, ok := getArg(basic, flags)
				if !ok {
					return nil, false
				}
				switch v := v.(type) {
				case int64:
					return v * int64(sign), true
				case uint64:
					if sign == -1 && v > 1<<63 {
						fmt.Fprintf(os.Stderr, "%s is out of range for int64", gofmt(x))
						return nil, false
					}
					return v * uint64(sign), true
				case float64:
					return v * float64(sign), true
				}
			}
		}

	case *ast.Ident:
		switch x.Name {
		case "Inf":
			if flags&forceFloat64 != 0 {
				return math.Inf(+1), true
			}
			return ordered.Inf, true
		case "NaN":
			if flags&forceFloat64 != 0 {
				return math.NaN(), true
			}
			fmt.Fprintf(os.Stderr, "must use float32(NaN) or float64(NaN)\n")
			return nil, false
		}

	case *ast.CallExpr:
		fn, ok := x.Fun.(*ast.Ident)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown call to %s\n", gofmt(x.Fun))
			return nil, false
		}
		if len(x.Args) != 1 {
			fmt.Fprintf(os.Stderr, "call to %s requires 1 argument\n", gofmt(x.Fun))
			return nil, false
		}
		switch fn.Name {
		default:
			fmt.Fprintf(os.Stderr, "unknown call to %s\n", fn.Name)
			return nil, false

		case "rev":
			if flags&noRev != 0 {
				fmt.Fprintf(os.Stderr, "invalid nested reverse\n")
				return nil, false
			}
			v, ok := getArg(x.Args[0], noRev)
			if !ok {
				return nil, false
			}
			return ordered.RevAny(v), true

		case "float32":
			v, ok := getArg(x.Args[0], noRev|forceFloat64)
			if !ok {
				return nil, false
			}
			return float32(v.(float64)), true

		case "float64":
			return getArg(x.Args[0], noRev|forceFloat64)
		}
	}
	fmt.Fprintf(os.Stderr, "invalid ordered value %s", gofmt(x))
	return nil, false
}

func decode(enc []byte) string {
	if s, err := ordered.DecodeFmt(enc); err == nil {
		return "o" + s
	}
	s := string(enc)
	if strconv.CanBackquote(s) {
		return "`" + s + "`"
	}
	return strconv.QuoteToGraphic(s)
}

var emptyFset = token.NewFileSet()

func gofmt(x ast.Expr) string {
	var buf bytes.Buffer
	err := printer.Fprint(&buf, emptyFset, x)
	if err != nil {
		return fmt.Sprintf("?err: %s?", err)
	}
	return buf.String()
}

func openDB(spec string) (storage.DB, error) {
	kind, args, _ := strings.Cut(spec, ":")
	dbInfo, namespace, _ := strings.Cut(args, ":")

	if namespace != "" {
		return nil, fmt.Errorf("namespaces (%s) supported only in vector (-vec) mode", namespace)
	}

	switch kind {
	case "pebble":
		return pebble.Open(slog.Default(), dbInfo)
	case "firestore":
		proj, db, _ := strings.Cut(dbInfo, ",")
		if proj == "" || db == "" {
			return nil, fmt.Errorf("invalid DB spec %s:%s; want 'firestore:PROJECT,DATABASE'", kind, dbInfo)
		}
		return firestore.NewDB(context.Background(), slog.Default(), proj, db)
	default:
		return nil, errors.New("unknown DB type")
	}
}

func readLine(t *term.Terminal) (string, error) {
	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	defer term.Restore(int(os.Stdin.Fd()), old)
	return t.ReadLine()
}
