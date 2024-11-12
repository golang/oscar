// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/oscar/internal/storage"
	"rsc.io/ordered"
)

var _ page = dbviewPage{}

// dbviewPage holds the fields needed to display a view of the database.
type dbviewPage struct {
	Form   dbviewForm // the raw form inputs
	Result *dbviewResult
	Error  error // if non-nil, the error to display instead of the result
}

type dbviewForm struct {
	Start, End string // comma-separated lists; see parseOredered for details
	Limit      int
}

type dbviewResult struct {
	Items []item
}

// An item is a string representation of a key-value pair.
type item struct {
	Key, Value string
}

var dbviewPageTmpl = newTemplate(dbviewPageTmplFile, nil)

func (g *Gaby) handleDBview(w http.ResponseWriter, r *http.Request) {
	g.slog.Info("handleDBView")
	handlePage(w, g.populateDBviewPage(r), dbviewPageTmpl)
}

// populateDBviewPage returns the contents of the dbView page.
func (g *Gaby) populateDBviewPage(r *http.Request) dbviewPage {
	limit := parseInt(r.FormValue("limit"), 100)
	p := dbviewPage{
		Form: dbviewForm{
			Start: r.FormValue("start"),
			End:   r.FormValue("end"),
			Limit: limit,
		},
	}
	start := parseOrdered(p.Form.Start)
	end := parseOrdered(p.Form.End)
	g.slog.Info("calling dbview", "limit", limit)
	res, err := g.dbview(start, end, limit)
	g.slog.Info("done")
	if err != nil {
		p.Error = err
		return p
	}
	p.Result = res
	return p
}

func (g *Gaby) dbview(start, end []byte, limit int) (*dbviewResult, error) {
	if len(start) == 0 && len(end) > 0 {
		return nil, errors.New("missing start key")
	}
	if len(start) > 0 && len(end) == 0 {
		val, ok := g.db.Get(start)
		if !ok {
			return nil, nil
		}
		return &dbviewResult{Items: []item{makeItem(start, val)}}, nil
	}
	var items []item
	for k, vf := range g.db.Scan(start, end) {
		g.slog.Info("item", "key", k)
		items = append(items, makeItem(k, vf()))
		if len(items) >= limit {
			break
		}
	}

	if len(items) == 0 {
		return nil, nil
	}
	return &dbviewResult{Items: items}, nil
}

func makeItem(k, v []byte) item {
	var sval string
	// If v consists of an ordered int64 followed by what might be a JSON object,
	// guess that it was created by the timed package.
	var t int64
	val, err := ordered.DecodePrefix(v, &t)
	if err == nil && len(val) > 0 && val[0] == '{' {
		sval = fmt.Sprintf("DBTime(%d)\n%s", t, fmtValue(val))
	} else {
		sval = storage.Fmt(v)
	}
	return item{Key: storage.Fmt(k), Value: sval}
}

// parseOrdered parses a comma-separated list into an [ordered] value.
// It returns nil on the empty string.
// Special cases for each comma-separated part are integers and the special strings
// "inf" or "-inf". Anything else is treated as a string.
func parseOrdered(s string) []byte {
	var parts []any
	words := strings.Split(s, ",")
	if len(words) == 1 && strings.TrimSpace(words[0]) == "" {
		return nil
	}
	for _, p := range words {
		var part any
		p = strings.TrimSpace(p)
		pl := strings.ToLower(p)
		if pl == "inf" {
			part = ordered.Inf
		} else if pl == "-inf" {
			part = ordered.Rev(ordered.Inf)
		} else if i, err := strconv.Atoi(p); err == nil {
			part = i
		} else {
			part = p
		}
		parts = append(parts, part)
	}
	return ordered.Encode(parts...)
}

// parseInt returns the int represented by s.
// If s does not represent an int, it returns defaultValue.
func parseInt(s string, defaultValue int) int {
	if i, err := strconv.Atoi(s); err == nil {
		return i
	}
	return defaultValue
}
