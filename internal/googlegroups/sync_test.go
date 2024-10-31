// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package googlegroups

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

// aizaRegexp is used to identify AIza Cloud credentials
// in test data.
var aizaRegexp = regexp.MustCompile("AIza[0-9A-Za-z_-]{35}")

// scrub is a response scrubber for use with [rsc.io/httprr].
func scrub(b *bytes.Buffer) error {
	body := b.String()
	b.Reset()
	newBody := aizaRegexp.ReplaceAllString(body, "")
	_, err := b.WriteString(newBody)
	return err
}

func TestSync(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	ctx := context.Background()

	rr, err := httprr.Open("testdata/sync.httprr", http.DefaultTransport)
	check(err)
	rr.ScrubResp(scrub)
	sdb := secret.Empty()
	c := New(lg, db, sdb, rr.Client())

	const group = "golang-announce"
	check(c.Add(group))

	convs := func() []*Conversation {
		var convs []*Conversation
		for _, cfn := range c.Conversations(group) {
			convs = append(convs, cfn())
		}
		return convs
	}

	// Only look at changes before a certain date, so that we
	// don't get too much data.
	testTomorrow = "2011-05-05"
	defer func() { testTomorrow = "" }()
	check(c.Sync(ctx))

	cs := convs()
	if len(cs) != 1 {
		t.Errorf("got %d conversations; want 1", len(cs))
	}
	if cs[0].Title != "release.r57.1" {
		t.Errorf("got %q as title; want 'release.r57.1'", cs[0].Title)
	}
	msgs := cs[0].Messages
	if len(msgs) != 1 {
		t.Errorf("got %d message; want 1", len(msgs))
	}

	const wantMessage = "just tagged our second stable Go release, release.r57.1."
	if html := msgs[0]; !strings.Contains(html, wantMessage) {
		t.Errorf("could not locate '%s' message in the %s message", wantMessage, cs[0].URL)
	}

	// Now go a little bit into the future, but there should
	// be no more conversations.
	testTomorrow = "2011-05-06"
	check(c.Sync(ctx))

	if got := len(convs()); got != 1 {
		t.Errorf("got %d conversations; want 1", got)
	}
}

func TestMatchConversationURL(t *testing.T) {
	const group = "golang-dev"

	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://groups.google.com/g/golang-dev/c/tXG6Jk_dos8", true},
		{"groups.google.com/g/golang-dev/c/tXG6Jk_dos8", false},
		{"https://groups.google.com/g/golang-nuts/c/tXG6Jk_dos8/m/my-groups", false}, // wrong group
		{"https://groups.google.com/g/golang-dev/c/tXG6Jk_dos8/m/g/golang-dev", false},
	} {
		if got := matchesConversation(tc.url, group); got != tc.want {
			t.Errorf("got match=%t for %s; want %t", got, tc.url, tc.want)
		}
	}
}

func TestConversationLink(t *testing.T) {
	const group = "golang-dev"

	for _, tc := range []struct {
		url  string
		want string
	}{
		{"https://groups.google.com/g/golang-dev/c/tXG6Jk_dos8", // conversation
			"https://groups.google.com/g/golang-dev/c/tXG6Jk_dos8"},
		{"https://groups.google.com/g/golang-dev/c/tXG6Jk_dos8/m/1234", // conversation message
			"https://groups.google.com/g/golang-dev/c/tXG6Jk_dos8"},
		{"https://groups.google.com/g/golang-dev/g/golang-dev/c/tXG6Jk_dos8/m/1234", // repeated group
			"https://groups.google.com/g/golang-dev/c/tXG6Jk_dos8"},
		{"https://groups.google.com/g/golang-dev/about",
			"https://groups.google.com/g/golang-dev/about"},
	} {
		if got := conversationLink(tc.url, group); got != tc.want {
			t.Errorf("got %s for %s; want %s", got, tc.url, tc.want)
		}
	}
}

func TestSyncTesting(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	ctx := context.Background()

	group := "test"
	numConvs := func(c *Client) int {
		cnt := 0
		for _, _ = range c.Conversations(group) {
			cnt++
		}
		return cnt
	}

	for _, d := range []struct {
		limit         int
		wantConvs     int
		wantInterrupt bool
	}{
		// interrupt is skipped since it should happen at the
		// second conversation of the day, but the day limit is 1.
		// Due to this limit, we sync only 1 conversation per day.
		{1, 4, false},
		// With increased limit, we can sync more conversations
		// per day.
		{2, 6, true},
		{3, 7, true},
	} {
		t.Run(fmt.Sprintf("%d", d.limit), func(t *testing.T) {
			db := storage.MemDB()
			c := New(lg, db, nil, nil)
			check(c.Add(group))

			tc := c.Testing()
			tc.setLimit(d.limit)
			check(tc.LoadTxtar("testdata/convs.txt"))

			err := c.Sync(ctx)
			if d.wantInterrupt {
				if err == nil || !strings.Contains(err.Error(), "test interrupt error") {
					t.Fatalf("want test interrupt error; got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			check(c.Sync(ctx)) // repeat without interruption

			if gotConvs := numConvs(c); gotConvs != d.wantConvs {
				t.Errorf("want %d conversations; got %d", d.wantConvs, gotConvs)
			}
		})
	}
}

func TestTitleAndMessages(t *testing.T) {
	check := testutil.Checker(t)
	h := `<html>
<head></head>
<body>
<script>some javascript</script>
<div id="go">Go</div>
<div aria-label="golang-test">
<section><div>Section 1</div></section>
<div><section>Section 2</section></div>
<section>Section 3<h1><section>Section 4</section></h1></section>
</div>
</body>
</html>`
	title, msgs, err := titleAndMessages([]byte(h))
	check(err)
	if len(msgs) != 4 {
		t.Errorf("got %d messages; want 4", len(msgs))
	}
	want := "<section><div>Section 1</div></section>"
	if msgs[0] != want {
		t.Errorf("got %q as the first message; want %s", msgs[0], want)
	}
	if title != "golang-test" {
		t.Errorf("got %q as title; want 'golang-test'", title)
	}
}

func TestTruncate(t *testing.T) {
	dls := dbLimitSize
	dbLimitSize = 120
	defer func() { dbLimitSize = dls }()

	c := &Conversation{
		URL:      "https://groups.google.com/g/golang/test/c/123456",
		Title:    "convo",
		Messages: []string{"open", "reply", "close"},
	}

	// Conversation c in JSON is 121 bytes big, so
	// only the last message should be truncated.
	if !truncate(c) {
		t.Error("want conversation truncated")
	}
	want := []string{"open", "reply"}
	if !slices.Equal(want, c.Messages) {
		t.Errorf("want %v messages; got %v", want, c.Messages)
	}

	if truncate(c) {
		t.Error("conversation truncation not idempotent")
	}
}
