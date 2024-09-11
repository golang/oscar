// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package crawldocs

import (
	"context"
	"os"
	"testing"

	"golang.org/x/oscar/internal/crawl"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestSync(t *testing.T) {
	ctx := context.Background()
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()

	data, err := os.ReadFile("testdata/toolchain.html")
	check(err)
	dc := docs.New(db)
	cr := crawl.New(lg, db, nil)
	cr.Set(&crawl.Page{
		URL:  "https://go.dev/doc/toolchain",
		HTML: data,
	})
	cr.Set(&crawl.Page{
		URL:  "https://go.dev/doc/empty",
		HTML: nil,
	})

	check(Sync(ctx, lg, dc, cr))

	var want = []string{
		"https://go.dev/doc/toolchain#GOTOOLCHAIN",
		"https://go.dev/doc/toolchain#config",
		"https://go.dev/doc/toolchain#download",
		"https://go.dev/doc/toolchain#get",
		"https://go.dev/doc/toolchain#intro",
		"https://go.dev/doc/toolchain#name",
		"https://go.dev/doc/toolchain#select",
		"https://go.dev/doc/toolchain#switch",
		"https://go.dev/doc/toolchain#version",
		"https://go.dev/doc/toolchain#work",
	}
	for d := range dc.Docs("") {
		if len(want) == 0 {
			t.Fatalf("unexpected extra doc: %s", d.ID)
		}
		if d.ID != want[0] {
			t.Fatalf("doc mismatch: have %s, want %s", d.ID, want[0])
		}
		want = want[1:]
		if d.ID == download {
			if d.Title != downloadTitle {
				t.Errorf("download Title = %q, want %q", d.Title, downloadTitle)
			}
			if d.Text != downloadText {
				t.Errorf("download Text = %q, want %q", d.Text, downloadText)
			}
		}
	}
	if len(want) > 0 {
		t.Fatalf("missing docs: %v", want)
	}

	dc.Add(download, "OLD TITLE", "OLD TEXT")
	check(Sync(ctx, lg, dc, cr))
	d, _ := dc.Get(download)
	if d.Title != "OLD TITLE" || d.Text != "OLD TEXT" {
		t.Errorf("Sync rewrote #download: Title=%q Text=%q, want OLD TITLE, OLD TEXT", d.Title, d.Text)
	}

	Restart(ctx, lg, cr)
	check(Sync(ctx, lg, dc, cr))
	d, _ = dc.Get(download)
	if d.Title == "OLD TITLE" || d.Text == "OLD TEXT" {
		t.Errorf("Restart+Sync did not rewrite #download: Title=%q Text=%q", d.Title, d.Text)
	}
}

var (
	download      = "https://go.dev/doc/toolchain#download"
	downloadTitle = "Go Toolchains > Downloading toolchains"
	downloadText  = "When using GOTOOLCHAIN=auto or GOTOOLCHAIN=<name>+auto, the Go command\ndownloads newer toolchains as needed.\nThese toolchains are packaged as special modules\nwith module path golang.org/toolchain\nand version v0.0.1-goVERSION.GOOS-GOARCH.\nToolchains are downloaded like any other module,\nmeaning that toolchain downloads can be proxied by setting GOPROXY\nand have their checksums checked by the Go checksum database.\nBecause the specific toolchain used depends on the system’s own\ndefault toolchain as well as the local operating system and architecture (GOOS and GOARCH),\nit is not practical to write toolchain module checksums to go.sum.\nInstead, toolchain downloads fail for lack of verification if GOSUMDB=off.\nGOPRIVATE and GONOSUMDB patterns do not apply to the toolchain downloads."
)
