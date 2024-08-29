// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"

	"golang.org/x/oscar/internal/storage"
)

func TestSearchResultsHTML(t *testing.T) {
	query := "some query"
	searchResults := []searchResult{
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
	}
	gotb, err := searchResultsHTML(query, searchResults)
	if err != nil {
		t.Fatal(err)
	}
	wants := []string{query}
	for _, sr := range searchResults {
		wants = append(wants, sr.VResult.ID)
	}
	got := string(gotb)
	t.Logf("%s", got)
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("did not find %q in HTML", w)
		}
	}
}
