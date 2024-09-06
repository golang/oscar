// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/storage"
)

func TestSearchPageTemplate(t *testing.T) {
	page := searchPage{
		Query: "some query",
		Results: []searchResult{
			{
				Title: "t1",
				VResult: storage.VectorResult{
					ID:    "https://example.com/x",
					Score: 0.987654321,
				},
				IDIsURL: true,
			},
			{
				VResult: storage.VectorResult{
					ID:    "https://example.com/y",
					Score: 0.876,
				},
				IDIsURL: false,
			},
		},
	}

	var buf bytes.Buffer
	if err := searchPageTmpl.Execute(&buf, page); err != nil {
		t.Fatal(err)
	}
	wants := []string{page.Query}
	for _, sr := range page.Results {
		wants = append(wants, sr.VResult.ID)
	}
	got := buf.String()
	t.Logf("%s", got)
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("did not find %q in HTML", w)
		}
	}
}
