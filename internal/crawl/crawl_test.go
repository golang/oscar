// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package crawl

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestCrawl(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()

	newCrawl := func(tc *http.Client) *Crawler {
		c := New(lg, db, tc)
		c.Allow(allow...)
		c.Deny(deny...)
		c.Clean(clean)
		c.Add("https://go.dev/")
		c.Add("https://go.dev/")
		return c
	}

	tc := readTestClient(t, "testdata/godev.txt")
	c := newCrawl(tc)

	testutil.StopPanic(func() {
		c.Add("https://go.dev/#foo")
		t.Errorf("Add with URL fragment did not panic")
	})

	check(c.Run(context.Background()))

	for _, u := range needVisited {
		if p, ok := c.Get(u); !ok {
			t.Errorf("Crawl %s: should have visited, did not", u)
		} else if len(p.HTML) > 0 {
			t.Errorf("Crawl %s: should have visited but not recorded HTML; found HTML", u)
		}
	}
	for _, u := range needHTML {
		if p, ok := c.Get(u); !ok {
			t.Errorf("Crawl %s: should have recorded HTML, did not visit", u)
		} else if len(p.HTML) == 0 {
			t.Errorf("Crawl %s: should have recorded HTML, visited but no HTML", u)
		}
	}
	for _, u := range needSkipped {
		if _, ok := c.Get(u); ok {
			t.Errorf("Crawl %s: should have skipped, found queued page", u)
		}
	}

	// Check for various errors.
	for p := range c.PageWatcher("test1").Recent() {
		if strings.Contains(p.URL, "/err/") && p.Error == "" {
			t.Errorf("crawl %s: no error", p.URL)
		}
	}

	// Check that default recrawl does not recrawl.
	didRoot2 := false
	c = newCrawl(&http.Client{
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/root2" {
				didRoot2 = true
				return tc.Transport.RoundTrip(req)
			}
			t.Fatalf("crawler recrawled too soon")
			panic("unreachable")
		}),
	})
	c.Add("https://go.dev/root2") // not seen yet
	check(c.Run(context.Background()))
	if !didRoot2 {
		t.Errorf("did not crawl /root2")
	}

	// Check that Recrawl(0) does recrawl, but also terminates.
	n := 0
	c = newCrawl(&http.Client{
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			n++
			return tc.Transport.RoundTrip(req)
		}),
	})
	c.SetRecrawl(0)
	check(c.Run(context.Background()))
	if n < 3 {
		t.Fatalf("Run after Recrawl(0) crawled %d pages, want ≥ 3", n)
	}
}

var allow = []string{
	"https://go.dev/",
}

var deny = []string{
	"https://go.dev/api/",
	"https://go.dev/change/",
	"https://go.dev/cl/",
	"https://go.dev/design/",
	"https://go.dev/dl/",
	"https://go.dev/issue/",
	"https://go.dev/lib/",
	"https://go.dev/misc/",
	"https://go.dev/play",
	"https://go.dev/s/",
	"https://go.dev/src/",
	"https://go.dev/test/",
}

var needVisited = []string{
	"https://go.dev/doc/faq/",
	"https://go.dev/err/bad-status",
	"https://go.dev/err/bad-content-type",
	"https://go.dev/err/redirect-no-location",
	"https://go.dev/err/redirect-bad-url",
	"https://go.dev/err/body-too-large",
	"https://go.dev/err/body-read-error",
}

var needHTML = []string{
	"https://go.dev/",
	"https://go.dev/doc/faq",
	"https://go.dev/pkg/math/?m=old",
	"https://go.dev/pkg/strings/?m=old",
	"https://go.dev/player/okay",
}

var needSkipped = []string{
	"https://go.dev/play/p/asdf",
	"https://go.dev/s/short",
	"https://www.google.com/",
	"https://go.dev/root2",
	"https://go.dev/err/clean-error",
	"https://go.dev/err/disallow-after-clean",
}

func clean(u *url.URL) error {
	if u.Host == "go.dev" {
		u.RawQuery = ""
		u.ForceQuery = false
		if strings.HasPrefix(u.Path, "/pkg") || strings.HasPrefix(u.Path, "/cmd") {
			u.RawQuery = "m=old"
		}
	}

	if strings.HasPrefix(u.Path, "/err/disallow-after-clean") {
		u.Path = "/s/disallow"
	}

	if strings.HasPrefix(u.Path, "/err/clean-error") {
		return errors.New("clean error!")
	}
	return nil
}

func TestLinks(t *testing.T) {
	check := testutil.Checker(t)

	u, err := url.Parse("https://go.dev/")
	check(err)
	doc, err := html.Parse(strings.NewReader(`
		<a href="a1"></a><a href="https://www.google.com/a2"></a><a href="a3"></a><a href="a4"></a>
	`))
	check(err)
	want := []string{
		"https://go.dev/a1",
		"https://www.google.com/a2",
		"https://go.dev/a3",
		"https://go.dev/a4",
	}
	var have []string
	for u := range links(testutil.Slogger(t), u, doc) {
		have = append(have, u.String())
	}
	if !slices.Equal(have, want) {
		t.Errorf("links:\nhave %q\nwant %q", have, want)
	}

	want = want[:2]
	have = have[:0]
	for u := range links(testutil.Slogger(t), u, doc) {
		have = append(have, u.String())
		if len(have) == 2 {
			break
		}
	}
	if !slices.Equal(have, want) {
		t.Errorf("links with early break:\nhave %q\nwant %q", have, want)
	}
}
