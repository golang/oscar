// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import (
	"context"
	"testing"

	"golang.org/x/oscar/internal/llm"
)

func TestOverview(t *testing.T) {
	ctx := context.Background()
	g := llm.EchoTextGenerator()
	d1 := &Doc{URL: "https://example.com", Author: "rsc", Title: "title", Text: "some text"}
	d2 := &Doc{Text: "some text 2"}
	got, err := Overview(ctx, g, d1, d2)
	if err != nil {
		t.Fatal(err)
	}
	want := llm.EchoResponse(
		`{"url":"https://example.com","author":"rsc","title":"title","text":"some text"}`,
		`{"text":"some text 2"}`,
		instruction)
	if got != want {
		t.Errorf("Overview() = %s, want %s", got, want)
	}
	t.Log(want)
}
