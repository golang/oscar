// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package crawl implements a basic web crawler for crawling a portion of a web site.
// Construct a [Crawler], configure it, and then call its [Run] method.
// The crawler stores the crawled data in a [storage.DB], and then
// [Crawler.PageWatcher] can be used to watch for new pages.
package crawl

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

// This package stores timed entries in the database of the form:
//
//	["crawl.Page", URL] => [Raw(JSON(Page)), Raw(HTML)]
//
// The HTML is the raw HTML served at URL.
// Storing the raw HTML avoids having to re-download the site each time
// we change the way the HTML is processed.
// JSON and HTML are empty if the page has been found but not yet crawled.

const crawlKind = "crawl.Page"

const defaultRecrawl = 24 * time.Hour

// A Crawler is a basic web crawler.
//
// Note that this package does not load or process robots.txt.
// Instead the assumption is that the site owner is crawling a portion of their own site
// and will confiure the crawler appropriately.
// (In the case of Go's Oscar instance, we only crawl go.dev.)
type Crawler struct {
	slog    *slog.Logger
	db      storage.DB
	http    *http.Client
	recrawl time.Duration
	cleans  []func(*url.URL) error
	rules   []rule
}

// A rule is a rule about which URLs can be crawled.
// See [Crawler.Allow] for more details.
type rule struct {
	prefix string // URLs matching this prefix should be ...
	allow  bool   // allowed or disallowed
}

// TODO(rsc): Store ETag and use to avoid redownloading?

// A Page records the result of crawling a single page.
type Page struct {
	DBTime    timed.DBTime
	URL       string    // URL of page
	From      string    // a page where we found the link to this one
	LastCrawl time.Time // time of last crawl
	Redirect  string    // HTTP redirect during fetch
	HTML      []byte    // HTML content, if any
	Error     string    // error fetching page, if any
}

var _ docs.Entry = (*Page)(nil)

// LastWritten implements [docs.Entry.LastWritten].
func (p *Page) LastWritten() timed.DBTime {
	return p.DBTime
}

// A crawlPage is the JSON form of Page.
// The fields and field order of crawlPage and Page must match exactly; only the struct tags differ.
// We omit the DBTime, URL, and HTML fields from JSON, because they are encoded separately.
// Using this separate copy of the struct avoids forcing the internal JSON needs of this package
// onto clients using Page.
type crawlPage struct {
	DBTime    timed.DBTime `json:"-"`
	URL       string       `json:"-"`
	From      string
	LastCrawl time.Time
	Redirect  string
	HTML      []byte `json:"-"`
	Error     string
}

// New returns a new [Crawler] that uses the given logger, database, and HTTP client.
// The caller should configure the Crawler further by calling [Crawler.Add],
// [Crawler.Allow], [Crawler.Deny], [Crawler.Clean], and [Crawler.SetRecrawl].
// Once configured, the crawler can be run by calling [Crawler.Run].
func New(lg *slog.Logger, db storage.DB, hc *http.Client) *Crawler {
	if hc != nil {
		// We want a client that does not follow redirects,
		// but we cannot modify the caller's http.Client directly.
		// Instead, make our own copy and override CheckRedirect.
		hc1 := *hc
		hc = &hc1
		hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}

	c := &Crawler{
		slog:    lg,
		db:      db,
		http:    hc,
		recrawl: defaultRecrawl,
	}
	return c
}

// Add adds the URL to the list of roots for the crawl.
// The added URL must not include a URL fragment (#name).
func (c *Crawler) Add(url string) {
	if strings.Contains(url, "#") {
		panic("crawl misuse: Add of URL with fragment")
	}
	if _, ok := c.Get(url); ok {
		return
	}
	b := c.db.Batch()
	c.set(b, &Page{URL: url})
	b.Apply()
}

// SetRecrawl sets the time to wait before recrawling a page.
// The default is 24 hours.
func (c *Crawler) SetRecrawl(d time.Duration) {
	c.recrawl = d
}

// decodePage decodes the timed.Entry into a Page.
func (c *Crawler) decodePage(e *timed.Entry) *Page {
	var p Page

	if err := ordered.Decode(e.Key, &p.URL); err != nil {
		// unreachable unless database corruption
		c.db.Panic("decode crawl.Page key", "key", storage.Fmt(e.Key), "err", err)
	}

	// The HTML is stored separately from the JSON describing the rest of the Page
	// to avoid the bother and overhead of JSON-encoding the HTML.
	var js, html ordered.Raw
	if err := ordered.Decode(e.Val, &js, &html); err != nil {
		// unreachable unless database corruption
		c.db.Panic("decode crawl.Page val", "val", storage.Fmt(e.Val), "err", err)
	}
	if len(js) > 0 {
		if err := json.Unmarshal(js, (*crawlPage)(&p)); err != nil {
			// unreachable unless database corruption
			c.db.Panic("decode crawl.Page js", "js", storage.Fmt(js), "err", err)
		}
	}

	p.HTML = html
	p.DBTime = e.ModTime
	return &p
}

// Get returns the result of the most recent crawl for the given URL.
// If the page has been crawled, Get returns a non-nil *Page, true.
// If the page has not been crawled, Get returns nil, false.
func (c *Crawler) Get(url string) (*Page, bool) {
	e, ok := timed.Get(c.db, crawlKind, ordered.Encode(url))
	if !ok {
		return nil, false
	}
	return c.decodePage(e), true
}

// Set adds p to the crawled page database.
// It is typically only used for setting up tests.
func (c *Crawler) Set(p *Page) {
	b := c.db.Batch()
	c.set(b, p)
	b.Apply()
}

// set records p in the batch b.
func (c *Crawler) set(b storage.Batch, p *Page) {
	if strings.Contains(p.URL, "#") {
		// Unreachable without logic bug in this package.
		panic("crawl misuse: Set of URL with fragment")
	}
	timed.Set(c.db, b, crawlKind,
		ordered.Encode(p.URL),
		ordered.Encode(
			ordered.Raw(storage.JSON((*crawlPage)(p))),
			ordered.Raw(p.HTML)))
}

// Run crawls all the pages it can, returning when the entire site has been
// crawled either during this run or within the crawl duration set by
// [Crawler.Recrawl].
func (c *Crawler) Run(ctx context.Context) error {
	// Crawl every page in the database.
	// The pages-by-time list serves as a work queue,
	// but if there are link loops we may end up writing a Page
	// we've already processed, making it appear again in our scan.
	// We use the crawled map to make sure we only crawl each page at most once.
	// We use the queued map to make sure we only queue each found link at most once.
	crawled := make(map[string]bool)
	queued := make(map[string]bool)
	for e := range timed.ScanAfter(c.slog, c.db, crawlKind, 0, nil) {
		p := c.decodePage(e)
		if time.Since(p.LastCrawl) < c.recrawl || crawled[p.URL] {
			continue
		}
		crawled[p.URL] = true
		c.crawlPage(ctx, queued, p)
	}
	return nil
}

// crawlPage downloads the content for a page,
// saves it, and then queues all links it can find in that page's HTML.
func (c *Crawler) crawlPage(ctx context.Context, queued map[string]bool, p *Page) {
	var slogBody []byte
	slog := c.slog.With("page", p.URL, "lastcrawl", p.LastCrawl)

	if strings.Contains(p.URL, "#") {
		// Unreachable without logic bug in this package.
		panic("crawl misuse: crawlPage of URL with fragment")
	}

	b := c.db.Batch()
	defer func() {
		if p.Error != "" {
			if slogBody != nil {
				slog = slog.With("body", string(slogBody[:min(len(slogBody), 1<<10)]))
			}
			slog.Warn("crawl error", "err", p.Error, "last", p.LastCrawl)
		}

		c.set(b, p)
		b.Apply()
		c.db.Flush()
	}()

	p.LastCrawl = time.Now()
	p.Redirect = ""
	p.Error = ""
	p.HTML = nil

	base, err := url.Parse(p.URL)
	if err != nil {
		// Unreachable unless Page was corrupted.
		p.Error = err.Error()
		return
	}

	u := base.String()
	slog = slog.With("url", u)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		// Unreachable unless url.String doesn't round-trip back to url.Parse.
		p.Error = err.Error()
		return
	}
	resp, err := c.http.Do(req)
	if err != nil {
		p.Error = err.Error()
		return
	}

	// TODO(rsc): Make max body length adjustable by policy.
	// Also set HTML tokenizer max? For now the max body length
	// takes care of it for us.
	const maxBody = 4 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	resp.Body.Close()
	slogBody = body
	if err != nil {
		p.Error = err.Error()
		return
	}
	if len(body) > maxBody {
		p.Error = "body too big"
		return
	}

	slog = slog.With("status", resp.Status)

	if resp.StatusCode/10 == 30 { // Redirect
		loc := resp.Header.Get("Location")
		if loc == "" {
			p.Error = "redirect without location"
			return
		}
		slog = slog.With("location", loc)

		locURL, err := url.Parse(loc)
		if err != nil {
			// Unreachable: http.Client.Do processes the Location header
			// to decide about following redirects (we disable that, but that
			// check only happens after the Location header is processed),
			// and will return an error if the Location has a bad URL.
			p.Error = err.Error()
			return
		}

		link := base.ResolveReference(locURL)
		p.Redirect = link.String()
		slog.Info("crawl redirect", "link", p.Redirect)
		c.queue(queued, b, link, u)
		return
	}
	if resp.StatusCode != 200 {
		p.Error = "http status " + resp.Status
		return
	}

	slogBody = nil

	ctype := resp.Header.Get("Content-Type")
	if ctype != "text/html" && !strings.HasPrefix(ctype, "text/html;") {
		slog = slog.With("content-type", ctype)
		p.Error = "Content-Type: " + ctype
		return
	}

	p.HTML = body
	slog = slog.With("htmlsize", len(body))
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		// Unreachable because it's either a read error
		// (but bytes.NewReader has no read errors)
		// or hitting the max HTML token limit (but we didn't set that limit).
		p.Error = "html parse error: " + err.Error()
		return
	}

	for link := range links(slog, base, doc) {
		if queued[link.String()] {
			// Quiet skip to avoid tons of repetitive logging about
			// all the links in the page footers.
			// (Calling c.queue will skip too but also log.)
			continue
		}
		slog.Info("crawl html link", "link", link)
		c.queue(queued, b, link, u)
	}
	slog.Info("crawl ok")
}

// queue queues the link for crawling, unless it has already been queued.
// It records that the link came from a page with URL fromURL.
func (c *Crawler) queue(queued map[string]bool, b storage.Batch, link *url.URL, fromURL string) {
	old := link.String()
	if queued[old] {
		return
	}
	queued[old] = true
	if err := c.clean(link); err != nil {
		c.slog.Info("crawl queue clean error", "url", old, "from", fromURL, "err", err)
		return
	}
	targ := link.String()
	if targ != old && queued[targ] {
		c.slog.Info("crawl queue seen", "url", targ, "old", old, "from", fromURL)
		return
	}
	queued[targ] = true
	if !c.allowed(targ) {
		c.slog.Info("crawl queue disallow after clean", "url", targ, "old", old, "from", fromURL)
		return
	}

	if strings.Contains(targ, "#") {
		// Unreachable without logic bug in this package.
		panic("crawl misuse: queue of URL with fragment")
	}
	p := &Page{
		URL:  targ,
		From: fromURL,
	}

	if old, ok := c.Get(targ); ok {
		if time.Since(old.LastCrawl) < c.recrawl {
			c.slog.Debug("crawl queue already visited", "url", targ, "last", old.LastCrawl)
			return
		}
		old.From = p.From
		p = old
	}

	c.slog.Info("crawl queue", "url", p.URL, "old", old)
	c.set(b, p)
}

// links returns an iterator over all HTML links in the doc,
// interpreted relative to base.
// It logs unexpected bad URLs to slog.
func links(slog *slog.Logger, base *url.URL, doc *html.Node) iter.Seq[*url.URL] {
	return func(yield func(*url.URL) bool) {
		// Walk HTML looking for <a href=...>.
		var yieldLinks func(*html.Node) bool
		yieldLinks = func(n *html.Node) bool {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if !yieldLinks(c) {
					return false
				}
			}
			var targ string
			if n.Type == html.ElementNode {
				switch n.Data {
				case "a":
					targ = findAttr(n, "href")
				}
			}
			// Ignore no target or #fragment.
			if targ == "" || strings.HasPrefix(targ, "#") {
				return true
			}

			// Parse target as URL.
			u, err := url.Parse(targ)
			if err != nil {
				slog.Info("links bad url", "base", base.String(), "targ", targ, "err", err)
				return true
			}
			return yield(base.ResolveReference(u))
		}
		yieldLinks(doc)
	}
}

// findAttr returns the value for n's attribute with the given name.
func findAttr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// Clean adds a cleaning function to the crawler's list of cleaners.
// Each time the crawler considers queuing a URL to be crawled,
// it calls the cleaning functions to canonicalize or otherwise clean the URL first.
// A cleaning function might remove unnecessary URL parameters or
// canonicalize host names or paths.
// The Crawler automatically removes any URL fragment before applying registered cleaners.
func (c *Crawler) Clean(clean func(*url.URL) error) {
	c.cleans = append(c.cleans, clean)
}

// allowed reports whether c's configuration allows the target URL.
func (c *Crawler) allowed(targ string) bool {
	allow := false
	n := 0
	for _, r := range c.rules {
		if n <= len(r.prefix) && hasPrefix(targ, r.prefix) {
			allow = r.allow
			n = len(r.prefix)
		}
	}
	return allow
}

// Allow records that the crawler is allowed to crawl URLs with the given list of prefixes.
// A URL is considered to match a prefix if one of the following is true:
//
//   - The URL is exactly the prefix.
//   - The URL begins with the prefix, and the prefix ends in /.
//   - The URL begins with the prefix, and the next character in the URL is / or ?.
//
// The companion function [Crawler.Deny] records that the crawler is not allowed to
// crawl URLs with a list of prefixes. When deciding whether a URL can be crawled,
// longer prefixes take priority over shorter prefixes.
// If the same prefix is added to both [Crawler.Allow] and [Crawler.Deny],
// the last call wins. The default outcome is that a URL is not
// allowed to be crawled.
//
// For example, consider this call sequence:
//
//	c.Allow("https://go.dev/a/")
//	c.Allow("https://go.dev/a/b/c")
//	c.Deny("https://go.dev/a/b")
//
// Given these rules, the crawler makes the following decisions about these URLs:
//
//   - https://go.dev/a: not allowed
//   - https://go.dev/a/: allowed
//   - https://go.dev/a/?x=1: allowed
//   - https://go.dev/a/x: allowed
//   - https://go.dev/a/b: not allowed
//   - https://go.dev/a/b/x: not allowed
//   - https://go.dev/a/b/c: allowed
//   - https://go.dev/a/b/c/x: allowed
//   - https://go.dev/x: not allowed
func (c *Crawler) Allow(prefix ...string) {
	for _, p := range prefix {
		c.rules = append(c.rules, rule{p, true})
	}
}

// Deny records that the crawler is allowed to crawl URLs with the given list of prefixes.
// See the [Crawler.Allow] documentation for details about prefixes and interactions with Allow.
func (c *Crawler) Deny(prefix ...string) {
	for _, p := range prefix {
		c.rules = append(c.rules, rule{p, false})
	}
}

// hasPrefix reports whether targ is considered to have the given prefix,
// following the rules documented in [Crawler.Allow]'s doc comment.
func hasPrefix(targ, prefix string) bool {
	if !strings.HasPrefix(targ, prefix) {
		return false
	}
	if len(targ) == len(prefix) || prefix != "" && prefix[len(prefix)-1] == '/' {
		return true
	}
	switch targ[len(prefix)] {
	case '/', '?':
		return true
	}
	return false
}

// clean removes the URL Fragment and then calls the registered cleaners on u.
// If any cleaner returns an error, clean returns that error and does not run any more cleaners.
func (c *Crawler) clean(u *url.URL) error {
	u.Fragment = ""
	for _, fn := range c.cleans {
		if err := fn(u); err != nil {
			return err
		}
	}
	return nil
}

// PageWatcher returns a timed.Watcher over Pages that the Crawler
// has stored in its database.
func (c *Crawler) PageWatcher(name string) *timed.Watcher[*Page] {
	return timed.NewWatcher(c.slog, c.db, name, crawlKind, c.decodePage)
}
