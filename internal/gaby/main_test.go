// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"
	"golang.org/x/oscar/internal/testutil"
)

func TestNewServer(t *testing.T) {
	g := &Gaby{
		ctx:       context.Background(),
		slog:      testutil.Slogger(t),
		slogLevel: new(slog.LevelVar),
		meter:     noop.Meter{},
	}

	// create in-memory test server
	report := func(err error) { t.Error(err) }
	mux := g.newServer(report)
	s := httptest.NewServer(mux)
	defer s.Close()

	read := func(r *http.Response) string {
		b, _ := ioutil.ReadAll(r.Body)
		r.Body.Close()
		return string(b)
	}

	// check "/" endpoint
	res, err := s.Client().Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	got := read(res)
	for _, want := range []string{"Gaby", "meta", "flags", "log level"} {
		if !strings.Contains(got, want) {
			t.Errorf("response for '/' endpoint expected to contain %s; got %s", want, got)
		}
	}

	// check "/setlevel" endpoint
	_, err = s.Client().Get(fmt.Sprintf("%s/setlevel?l=error", s.URL))
	if err != nil {
		t.Fatal(err)
	}
}
