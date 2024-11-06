// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package googlegroups saves google group conversations as HTML
// in a [storage.DB].
// Every Google Group has a unique name. Group conversations are
// identified with a URL of the form
// https://groups.google.com/g/<group name>/c/<conversation id>.
// The URL points to the Google Group web page for the conversation.
// The page contains all individual conversation messages.
package googlegroups

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/oscar/internal/crawl"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

const (
	syncGroupKind          = "google.SyncGroup"
	conversationKind       = "google.GroupConversation"
	conversationUpdateKind = "google.GroupConversationUpdate"
)

// This package stores timed entries in the database of the form:
//
//	["google.SyncGroup", group] => JSON of groupSync structure
//	["google.GroupConversation", group, URL] => Conversation JSON
//      ["google.GroupConversationUpdateByTime", DBTime, group, URL] => []
//
// Google Groups do not have an API for querying groups or conversations.
// Further, iterating through conversations via web page is not possible
// via URLs. One has to explicitly ask the page for more conversations.
//
// We sync the conversations then as follows. The algorithm asks the
// Group search page for all conversations updated today (high and current
// watermark) or earlier. It syncs conversations for today by crawling
// the search page and then proceeds by asking for conversations updated
// yesterday (updated current watermark) or earlier, and so on. Once the search
// page returns no conversations, the algorithm stops and remembers where
// it initially started from (lower watermark): the next invocation of
// the algorithm will look only for conversations updated after that point.
// Each conversation is represented by its raw HTML.
//
// The algorithm has a limitation that it will only sync 30 most recently
// updated conversations for a day. This is because Google Search page
// shows only 30 recently updated conversations.

// o is short for ordered.Encode.
func o(list ...any) []byte { return ordered.Encode(list...) }

// A Client is a connection to google groups, and to the database
// that stores information gathered from the groups.
type Client struct {
	slog   *slog.Logger
	db     storage.DB
	secret secret.DB
	http   *http.Client

	flushRequested atomic.Bool // flush database to disk when convenient

	testing bool

	testMu     sync.Mutex
	testClient *TestingClient
}

// New returns a new client to access google groups.
// The client uses the given logger, databases, and HTTP client.
//
// The secret database will look for a secret whose name is the
// "googlegroups" instance. The value will be user:pass. This is not yet used.
func New(lg *slog.Logger, db storage.DB, sdb secret.DB, hc *http.Client) *Client {
	return &Client{
		slog:    lg,
		db:      db,
		secret:  sdb,
		http:    hc,
		testing: testing.Testing(),
	}
}

// RequestFlush asks sync to flush the database to disk when
// convenient. This may be called concurrently with [Client.Sync].
func (c *Client) RequestFlush() {
	c.flushRequested.Store(true)
}

// groupSync records the sync state of a google group,
// such as "golang-dev" or "golang-announcements".
// This is stored in the database.
type groupSync struct {
	Name        string // group name, such as "golang-nuts" or "golang-dev".
	LowMark     string // low watermark: everything before this has been synced.
	HighMark    string // high watermark: everything after this has not been synced.
	CurrentMark string // current watermark: everything between this and HighMark has been synced.
}

// store stores group into db.
func (group *groupSync) store(db storage.DB) {
	db.Set(o(syncGroupKind, group.Name), storage.JSON(group))
}

// Add adds a google group such as "golang-dev" to the database.
// It only adds the group sync metadata.
// The initial data fetch does not happen until [Sync] or [SyncGroup]
// is called.
// Add returns an error if the project has already been added.
func (c *Client) Add(group string) error {
	key := o(syncGroupKind, group)
	if _, ok := c.db.Get(key); ok {
		return fmt.Errorf("ggroups.Add: already added: %q", group)
	}
	grp := &groupSync{
		Name: group,
	}
	c.db.Set(key, storage.JSON(grp))
	return nil
}

// Sync syncs the data for all client groups.
func (c *Client) Sync(ctx context.Context) error {
	var errs []error
	for key := range c.db.Scan(o(syncGroupKind), o(syncGroupKind, ordered.Inf)) {
		var group string
		if err := ordered.Decode(key, nil, &group); err != nil {
			c.db.Panic("ggroups client sync decode", "key", storage.Fmt(key), "err", err)
		}
		if err := c.SyncGroup(ctx, group); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// SyncGroup syncs a single group.
func (c *Client) SyncGroup(ctx context.Context, group string) (err error) {
	c.slog.Debug("ggroups.SyncGroup", "group", group)
	defer func() {
		if err != nil {
			err = fmt.Errorf("SyncGroup(%q): %w", group, err)
		}
	}()

	key := o(syncGroupKind, group)
	skey := string(key)

	// Lock the group, so that no else is sync'ing concurrently.
	c.db.Lock(skey)
	defer c.db.Unlock(skey)

	// Load sync state.
	var grp groupSync
	if val, ok := c.db.Get(key); !ok {
		return fmt.Errorf("missing group %s", group)
	} else if err := json.Unmarshal(val, &grp); err != nil {
		return err
	}

	return c.syncConversations(ctx, &grp)
}

// syncConversations syncs all group conversations updated in
// [group.HighMark, group.LowMark).
func (c *Client) syncConversations(ctx context.Context, group *groupSync) (err error) {
	save := func(low, high, curr string) {
		group.LowMark = low
		group.HighMark = high
		group.CurrentMark = curr
		group.store(c.db)
		c.db.Flush()
	}

	if group.HighMark == "" {
		// Since Google Groups intervals are at a day-level
		// granularity, we set the current mark to tomorrow,
		// so we can analyze updates made today.
		save(group.LowMark, now(), tomorrow())
	}

	c.slog.Info("ggroups sync", "group", group.Name, "low", group.LowMark,
		"curr", group.CurrentMark, "high", group.HighMark)
	if err := c.syncIntervalConversations(ctx, group); err != nil {
		return err
	}

	// Since Google Groups intervals are at a day-level granularity,
	// we set the low mark to day before the last day we analyzed.
	// For instance, the last day will in most cases be today. To
	// ensure that we analyze the rest of today on the next
	// invocation, we set the low mark to yesterday.
	yest, err := prev(group.HighMark)
	if err != nil {
		return err
	}
	save(yest, "", "")
	return nil
}

// testTomorrow exists for testing purposes, to avoid the
// issue of dealing with the current moment in time.
// For ordinary use this should be empty string.
// TODO: instead, should we ask database for its
// definition of tomorrow?
var testTomorrow string

// tomorrow returns day after the current time in
// timeStampLayout format.
func tomorrow() string {
	if testTomorrow != "" {
		return testTomorrow
	}
	return time.Now().Add(24 * time.Hour).Format(timeStampLayout)
}

// now returns the current time in
// timeStampLayout format.
func now() string {
	return time.Now().Format(timeStampLayout)
}

// syncIntervalConversations syncs conversations in (proj.CurrentMark, proj.LowMark).
func (c *Client) syncIntervalConversations(ctx context.Context, group *groupSync) error {
	b := c.db.Batch()
	defer func() {
		b.Apply()
		c.db.Flush()
	}()

	// We fetch increasingly smaller but overlapping conversation
	// intervals in order to ensure termination. Due to this and
	// concurrent modifications, we can see the same conversation
	// more than once. Keep track of the conversations we have
	// already seen.
	seen := make(map[string]bool)

	saveCurrentMark := func(curr string) {
		group.CurrentMark = curr
		group.store(c.db)
	}

	for {
		nConversations := 0
		c.slog.Info("ggroups interval sync", "group", group.Name, "low", group.LowMark, "curr", group.CurrentMark)
		for conv, err := range c.conversations(ctx, group.Name, group.LowMark, group.CurrentMark) {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			nConversations++

			if c.flushRequested.Load() {
				// Flush database.
				b.Apply()
				c.db.Flush()
				c.flushRequested.Store(false)
			}

			if seen[conv.URL] {
				continue
			}
			seen[conv.URL] = true

			key := o(conversationKind, group.Name, conv.URL)
			b.Set(key, storage.JSON(conv))
			// Record that the change was updated.
			timed.Set(c.db, b, conversationUpdateKind, o(group.Name, conv.URL), nil)

			b.MaybeApply()

			// Flush progress to the database occasionally
			// to make sure it is saved before interruption.
			if nConversations%10 == 0 {
				b.Apply()
				c.db.Flush()
			}
		}

		if nConversations == 0 {
			break
		}

		// Since conversations are returned as raw HTML, we
		// don't have the actual time they were updated. We
		// hence simply decrease the current mark by one day.
		pd, err := prev(group.CurrentMark)
		if err != nil {
			return err
		}
		saveCurrentMark(pd)
	}

	return nil
}

// prev accepts a timestamp t in timeStampLayout and
// returns a timestamp for exactly one day before t.
func prev(t string) (string, error) {
	tt, err := time.Parse(timeStampLayout, t)
	if err != nil {
		return "", err
	}
	return tt.Add(-24 * time.Hour).Format(timeStampLayout), nil
}

// dbSizeLimit is the limit on the value size
// that can be stored to firestore, in bytes.
// See https://firebase.google.com/docs/firestore/quotas
// for more information.
const dbSizeLimit = 1048576

// testDBSizeLimit is dbSizeLimit used for testing.
var testDBSizeLimit = 0

func conversationSizeLimit() int {
	if testDBSizeLimit != 0 {
		return testDBSizeLimit
	}
	return dbSizeLimit
}

// conversations returns an iterator, in reverse chronological order, over
// conversations updated in the interval (before, after).
func (c *Client) conversations(ctx context.Context, group, after, before string) iter.Seq2[*Conversation, error] {
	if c.divertChanges() { // testing
		return c.testClient.conversations(ctx, group, after, before)
	}

	return func(yield func(*Conversation, error) bool) {
		// Fetch all conversations by crawling the search page of the group.
		// Note: this approach has the limitation that only the 30 most recent
		// results will be returned.
		query := "before:" + before
		if after != "" {
			query += " after:" + after
		}
		values := url.Values{"q": []string{query}}
		addr := fmt.Sprintf("https://groups.google.com/g/%s/search?%s", group, values.Encode())

		db := storage.MemDB()
		crawler := crawl.New(c.slog, db, c.http)
		crawler.Add(addr)
		crawler.Allow("https://groups.google.com")

		if err := crawler.Run(ctx); err != nil {
			yield(nil, err)
			return
		}

		for p := range crawler.PageWatcher("ggroups").Recent() {
			// Google groups page contains, among other things,
			// links to the message updating the coversation,
			// but not the conversation link itself.
			u := conversationLink(p.URL, group)
			if !matchesConversation(u, group) {
				continue
			}

			// Fetch the body of the conversation since p.URL is
			// not pointing to the conversation page itself.
			html, err := getHTML(ctx, c.http, u)
			if err != nil {
				if !yield(nil, err) {
					return
				}
			} else {
				title, messages, err := titleAndMessages(c.slog, html)
				if err != nil {
					// unreachable unless error in crawler or html package
					c.db.Panic("ggroups extract messages", "conversation", u, "err", err)
				}
				conv := &Conversation{
					Group:    group,
					Title:    title,
					URL:      u,
					Messages: messages,
				}
				if len(messages) == 0 {
					// In case Google Groups HTML structure changes.
					c.slog.Error("ggroups conversation with no messages", "conversation", u)
				} else {
					if truncate(conv) {
						c.slog.Warn("ggroups conversation truncated", "conversation", u)
					}
					if !yield(conv, nil) {
						return
					}
				}
			}
		}
		return
	}
}

// conversationLink attempts to extract url of the
// conversation underlying u. Otherwise, returns u.
// A common example of u is the link to the first
// message of the conversation.
func conversationLink(u, group string) string {
	// Resolution of relative paths in the crawler
	// sometimes doubles up group component.
	from := fmt.Sprintf("/g/%s/g/%s/", group, group)
	to := fmt.Sprintf("/g/%s/", group)
	u = strings.Replace(u, from, to, 1)
	return strings.Split(u, "/m/")[0] // remove message suffix
}

// convRegexp is a regular expression that
// matches only Google Group conversation URLs.
var convRegexp = regexp.MustCompile("^https://groups.google.com/g/([^/]+)/c/[a-zA-Z0-9_]+$")

// matchesConversation checks if u is a
// conversation url for the group.
func matchesConversation(u, group string) bool {
	matches := convRegexp.FindAllStringSubmatch(u, -1)
	if len(matches) != 1 {
		return false
	}
	match := matches[0]
	if len(match) != 2 {
		return false
	}
	return match[1] == group
}

// getHTML uses hc to make an http GET request to u. It returns
// the raw body of the response. It does not follow redirections.
// TODO: extract common logic from crawl or simply use crawl?
func getHTML(ctx context.Context, hc *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, errors.New("http status " + resp.Status)
	}

	return body, nil
}

// titleAndMessages extracts HTML fragments of h
// containing individual conversation messages as
// well as the title.
// It returns an error if h is not HTML.
func titleAndMessages(lg *slog.Logger, h []byte) (string, []string, error) {
	root, err := html.Parse(bytes.NewReader(h))
	if err != nil {
		return "", nil, err
	}

	// Currently, Google Groups web page has individual
	// messages wrapped in top-level section HTML elements.
	sectionNodes := sections(root)
	if len(sectionNodes) == 0 {
		return "", nil, nil
	}

	// All sections are grouped together with a parent
	// that has "aria-label" set to the conversation
	// name. There does not seem to be a more structured
	// way of getting the title.
	var title string
	for _, a := range sectionNodes[0].Parent.Attr {
		if a.Key == "aria-label" {
			title = a.Val
			break
		}
	}

	var sections []string
	for _, s := range sectionNodes {
		clean(s) // reduce the size of each message
		var buf bytes.Buffer
		if err := html.Render(&buf, s); err != nil {
			lg.Error("ggroups failed section rendering", "err", err)
			continue
		}
		sections = append(sections, buf.String())
	}
	return title, sections, nil
}

// sections recursively collects all
// elements with section HTML tag.
func sections(n *html.Node) []*html.Node {
	var secs []*html.Node

	var doSec func(*html.Node)
	doSec = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "section" {
			secs = append(secs, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			doSec(c)
		}
	}
	doSec(n)

	return secs
}

// clean recursively removes tag attributes.
func clean(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		c.Attr = nil // clean attributes
		clean(c)
	}
}

// truncate removes trailing messages from c until
// the total size of c is below conversationLimitSize.
// It returns true if truncation happened, false otherwise.
func truncate(c *Conversation) bool {
	s := size(c)
	truncated := false
	limit := conversationSizeLimit()
	for s >= limit {
		if len(c.Messages) == 0 {
			// Sanity check, as this only happens if
			// conversation title and URL are together
			// of dbLimitSize.
			break
		}

		li := len(c.Messages) - 1
		lms := c.Messages[li]
		c.Messages = c.Messages[:li]
		truncated = true
		// Take into account quotation marks only.
		// Conservatively, do not take into account
		// potential spaces and commas around the message.
		s -= len(lms) + 2
	}
	return truncated
}

// size is the size of c in its
// db representation, in bytes.
func size(c *Conversation) int {
	return len(storage.JSON(c))
}
