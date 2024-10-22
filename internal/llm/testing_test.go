// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llm

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	ctx := context.Background()
	docs := []EmbedDoc{{Text: "abc"}, {Text: "alphabetical order"}}
	vecs, err := QuoteEmbedder().EmbedDocs(ctx, docs)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != len(docs) {
		t.Fatalf("len(docs) = %v, but len(vecs) = %d", len(docs), len(vecs))
	}
	for i, v := range vecs {
		u := UnquoteVector(v)
		if u != docs[i].Text {
			var buf strings.Builder
			for i, f := range v {
				fmt.Fprintf(&buf, " %f", f)
				if f < 0 {
					if i < len(v)-1 {
						fmt.Fprintf(&buf, " ... %f", v[len(v)-1])
					}
					break
				}
			}
			t.Logf("Embed(%q) = %v", docs[i].Text, buf.String())
			t.Errorf("Unquote() = %q, want %q", u, docs[i].Text)
		}
	}
}

func TestEcho(t *testing.T) {
	ctx := context.Background()
	gen := EchoTextGenerator()
	resp, err := gen.GenerateText(ctx, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp) != 1 {
		t.Fatalf("len(resp) = %v, want 1", len(resp))
	}
	if resp[0] != "abc" {
		t.Errorf("resp[0] = %q, want %q", resp[0], "abc")
	}
}
