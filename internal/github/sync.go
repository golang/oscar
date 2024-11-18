// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package github implements sync mechanism to mirror GitHub issue state
// into a [storage.DB] as well as code to inspect that state and to make
// issue changes on GitHub.
// All the functionality is provided by the [Client], created by [New].
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

const (
	eventKind       = "github.Event"
	syncProjectKind = "github.SyncProject"
)

// This package stores the following key schemas in the database:
//
//	["github.SyncProject", Project] => JSON of projectSync structure
//	["github.Event", Project, Issue, API, ID] => [DBTime, Raw(JSON)]
//	["github.EventByTime", DBTime, Project, Issue, API, ID] => []
//
// To reconstruct the history of a given issue, scan for keys from
// ["github.Event", Project, Issue] to ["github.Event", Project, Issue, ordered.Inf].
//
// The API field is "/issues", "/issues/comments", or "/issues/events",
// so the first key-value pair is the issue creation event with the issue body text.
//
// The IDs are GitHub's and appear to be ordered by time within an API,
// so that the comments are time-ordered and the events are time-ordered,
// but comments and events are not ordered with respect to each other.
// To order them fully, fetch all the events and sort by the time in the JSON.
//
// The JSON is the raw JSON served from GitHub describing the event.
// Storing the raw JSON avoids having to re-download everything if we decide
// another field is of interest to us.
//
// EventByTime is an index of Events by DBTime, which is the time when the
// record was added to the database. Code that processes new events can
// record which DBTime it has most recently processed and then scan forward in
// the index to learn about new events.

// o is short for ordered.Encode.
func o(list ...any) []byte { return ordered.Encode(list...) }

// Scrub is a scrubber for use with [rsc.io/httprr] when writing
// tests that access GitHub through an httprr.RecordReplay.
// It removes auth credentials from the request.
func Scrub(req *http.Request) error {
	req.Header.Del("Authorization")
	return nil
}

// A Client is a connection to GitHub state in a database and on GitHub itself.
type Client struct {
	slog   *slog.Logger
	db     storage.DB
	secret secret.DB
	http   *http.Client

	testing bool

	testMu     sync.Mutex
	testClient *TestingClient
	testEdits  []*TestingEdit
	testEvents map[string]json.RawMessage
}

// New returns a new client that uses the given logger, databases, and HTTP client.
//
// The secret database is expected to have a secret named "api.github.com" of the
// form "user:pass" where user is a user-name (ignored by GitHub) and pass is an API token
// ("ghp_...").
func New(lg *slog.Logger, db storage.DB, sdb secret.DB, hc *http.Client) *Client {
	return &Client{
		slog:    lg,
		db:      db,
		secret:  sdb,
		http:    hc,
		testing: testing.Testing(),
	}
}

var _ docs.Source[*Event] = (*Client)(nil)

const DocWatcherID = "githubdocs"

// DocWatcher returns the event watcher with name "githubdocs".
// Implements [docs.Source.DocWatcher].
func (c *Client) DocWatcher() *timed.Watcher[*Event] {
	return c.EventWatcher(DocWatcherID)
}

// ToDocs converts an event containing an issue to an
// embeddable document.
// It returns (nil, false) if the event is not an issue.
// Implements [docs.Source.ToDocs].
func (*Client) ToDocs(e *Event) (iter.Seq[*docs.Doc], bool) {
	issue, ok := e.Typed.(*Issue)
	if !ok {
		return nil, false
	}
	return slices.Values([]*docs.Doc{
		{
			ID:    issue.DocID(),
			Title: CleanTitle(issue.Title),
			Text:  CleanBody(issue.Body),
		},
	}), true
}

// A projectSync is per-GitHub project ("owner/repo") sync state stored in the database.
type projectSync struct {
	Name        string // owner/repo
	EventETag   string
	EventID     int64
	IssueDate   string
	CommentDate string
	RefillID    int64

	FullSyncActive bool
	FullSyncIssue  int64
}

// store stores proj into db.
func (proj *projectSync) store(db storage.DB) {
	db.Set(o(syncProjectKind, proj.Name), storage.JSON(proj))
}

// Add adds a GitHub project of the form
// "owner/repo" (for example "golang/go")
// to the database.
// It only adds the project sync metadata.
// The initial data fetch does not happen until [Sync] or [SyncProject] is called.
// If the project is already present, Add does nothing and returns nil.
func (c *Client) Add(project string) error {
	key := o(syncProjectKind, project)
	if _, ok := c.db.Get(key); ok {
		return nil
	}
	c.db.Set(key, storage.JSON(&projectSync{Name: project}))
	return nil
}

// Sync syncs all projects.
func (c *Client) Sync(ctx context.Context) error {
	for key := range c.db.Scan(o(syncProjectKind), o(syncProjectKind, ordered.Inf)) {
		var project string
		if err := ordered.Decode(key, nil, &project); err != nil {
			return fmt.Errorf("github client sync decode: key=%s err=%w", storage.Fmt(key), err)
		}
		if err := c.SyncProject(ctx, project); err != nil {
			c.slog.Error("github sync error", "project", project, "err", err)
		}
	}
	return nil
}

// If testFullSyncStop is non-nil, then SyncProject returns this error
// after each event is processed, to allow testing that interrupted syncs
// save state and can make progress.
var testFullSyncStop error

// SyncProject syncs a single project.
func (c *Client) SyncProject(ctx context.Context, project string) (err error) {
	c.slog.Debug("github.SyncProject", "project", project)
	defer func() {
		if err != nil {
			err = fmt.Errorf("SyncProject(%q): %w", project, err)
		}
	}()

	key := o(syncProjectKind, project)
	skey := string(key)

	// Lock the project, so that no one else is sync'ing
	// the project at the same time.
	c.db.Lock(skey)
	defer c.db.Unlock(skey)

	// Load sync state.
	var proj projectSync
	if val, ok := c.db.Get(key); !ok {
		return fmt.Errorf("missing project %v", project)
	} else if err := json.Unmarshal(val, &proj); err != nil {
		return err
	}

	// Sync issues, comments, events.
	if err := c.syncIssues(ctx, &proj); err != nil {
		return err
	}
	if err := c.syncIssueComments(ctx, &proj); err != nil {
		return err
	}

	// See syncIssueEvents doc comment for details about this dance.
	// The incremental event sync only works up to a certain number
	// of events. To initialize a repo, we need a “full sync” that scans one
	// issue at a time. We also need the full sync if we fall too far behind
	// by not syncing for many days.
	if proj.EventID == 0 || proj.FullSyncActive {
		// Full scan.
		if proj.EventID == 0 {
			proj.FullSyncActive = true
			proj.FullSyncIssue = 0
			proj.store(c.db)
			if err := c.syncIssueEvents(ctx, &proj, 0, true); err != nil {
				return err
			}
		}
		if err := c.syncIssues(ctx, &proj); err != nil {
			return err
		}
		for key := range c.db.Scan(o(eventKind, project), o(eventKind, project, ordered.Inf)) {
			var issue int64
			if _, err := ordered.DecodePrefix(key, nil, nil, &issue); err != nil {
				return err
			}
			if issue <= proj.FullSyncIssue {
				continue
			}
			if err := c.syncIssueEvents(ctx, &proj, issue, false); err != nil {
				return err
			}
			proj.FullSyncIssue = issue
			proj.store(c.db)
			if testFullSyncStop != nil {
				return testFullSyncStop
			}
		}
		// Fall through to incremental scan to clean up.
		proj.FullSyncActive = false
		proj.store(c.db)
	}

	// Incremental scan.
	if err := c.syncIssueEvents(ctx, &proj, 0, false); err != nil {
		return err
	}
	return nil
}

// syncIssues syncs the issues for a given project.
// It records all new issues since proj.IssueDate.
// If successful, it updates proj.IssueDate to the latest issue date seen.
func (c *Client) syncIssues(ctx context.Context, proj *projectSync) error {
	return c.syncByDate(ctx, proj, "/issues")
}

// syncIssueComments sync the issue comments for a given project.
// It records all new issue comments since proj.CommentDate.
// If successful, it updates proj.CommentDate to the latest comment date seen.
func (c *Client) syncIssueComments(ctx context.Context, proj *projectSync) error {
	return c.syncByDate(ctx, proj, "/issues/comments")
}

// syncByDate downloads and saves issues or issue comments since
// the date specified in proj (proj.IssueDate or proj.CommentDate).
// api is "/issues" for issues or "/issues/comments" for issue comments.
// syncByDate updates the proj date with the new latest date seen
// before any error.
func (c *Client) syncByDate(ctx context.Context, proj *projectSync, api string) error {
Restart:
	// For these APIs, we can ask GitHub for the event stream in increasing time order,
	// so we can iterate through all the events, saving the latest time we have seen,
	// and pick up where we left off.
	var since *string
	values := url.Values{
		"sort":      {"updated"},
		"direction": {"asc"},
		"page":      {"1"},
	}
	switch api {
	default:
		panic("downloadByDate api: " + api)
	case "/issues":
		since = &proj.IssueDate
		values["state"] = []string{"all"}
		values["per_page"] = []string{"100"}
	case "/issues/comments":
		since = &proj.CommentDate
	}
	if *since != "" {
		values["since"] = []string{*since}
	}

	b := c.db.Batch()
	defer b.Apply()

	urlStr := "https://api.github.com/repos/" + proj.Name + api + "?" + values.Encode()
	npage := 0
	defer proj.store(c.db)
	for pg, err := range c.pages(ctx, urlStr, "") {
		if err != nil {
			return err
		}

		for _, raw := range pg.body {
			var meta struct {
				ID        int64  `json:"id"`
				Updated   string `json:"updated_at"`
				Number    int64  `json:"number"`    // for /issues feed
				IssueURL  string `json:"issue_url"` // for /issues/comments feed
				CreatedAt string `json:"created_at"`
			}
			if err := json.Unmarshal(raw, &meta); err != nil {
				return fmt.Errorf("parsing JSON: %v", err)
			}
			if meta.ID == 0 {
				return fmt.Errorf("parsing message: no id: %s", string(raw))
			}
			if meta.Updated == "" {
				return fmt.Errorf("parsing JSON: no updated_at: %s", string(raw))
			}

			switch api {
			default:
				c.db.Panic("github downloadByDate bad api", "api", api)
			case "/issues":
				if meta.Number == 0 {
					return fmt.Errorf("parsing message: no number: %s", string(raw))
				}
			case "/issues/comments":
				n, err := strconv.ParseInt(meta.IssueURL[strings.LastIndex(meta.IssueURL, "/")+1:], 10, 64)
				if err != nil {
					return fmt.Errorf("invalid comment URL: %s", meta.IssueURL)
				}
				meta.Number = n
			}

			c.writeEvent(b, proj.Name, meta.Number, api, meta.ID, raw)
			b.MaybeApply()
			*since = meta.Updated
		}
		b.Apply()
		proj.store(c.db) // update *since

		// GitHub stops returning results after 1000 pages.
		// After 500 pages, restart pagination with a new since value.
		// TODO: Write a test.
		if npage++; npage >= 500 {
			goto Restart
		}
	}
	return nil
}

// syncIssueEvents downloads and saves new issue events in the given project.
//
// The /issues/events API does not have a "since time T" option:
// it lists events going backward in time, so we have to read backward
// until we find an event we've seen before. Only then can we update
// the "most recent seen event" proj.EventID, since otherwise there
// is still an unread gap between what we saw before and what we've
// seen so far. If syncIssueEvents is able to read back far enough, it
// updates proj.EventID and proj.EventETag.
//
// If onlySetLatest is true, syncIssueEvents does not store any events
// but does write down the most recent proj.EventID and proj.EventETag.
// (This doesn't make sense yet but keep reading.)
//
// The /issues/events API has a limit to how far back it can read,
// so if it has been a very long time since we last updated
// (or if this is the first sync), we may not reach the last known event.
// In that case, we have to resort to syncing each event separately
// using /issues/n/events, under the assumption that any one issue
// will be short enough to iterate its entire event list.
//
// If issue > 0, the sync uses /issues/n/events and does not update
// proj.EventID or proj.EventETag. The caller is expected to be looping
// over all events in this case.
//
// To sync all issues events in a repo with too many events, the full sync sequence is:
//
//   - c.syncIssueEvents(proj, 0, true) to set EventID and EventETag
//     to the current latest event in the repo, establishing a lower bound on what
//     we will record during the next steps.
//
//   - c.syncIssues(proj) to get the full list of issues.
//
//   - c.syncIssueEvents(proj, issue, false) for every issue found by syncIssues.
//
//     Now the database should contain all the events with id <= proj.EventID
//     but it may also contain newer ones.
//
//   - c.syncIssueEvents(proj, 0, false) to read any events since the beginning of the sync.
//
//     Now the database should contain all events up to the new proj.EventID.
func (c *Client) syncIssueEvents(ctx context.Context, proj *projectSync, issue int64, onlySetLatest bool) error {
	if issue > 0 && onlySetLatest {
		panic("syncIssueEvents misuse")
	}

	values := url.Values{
		"page":     {"1"},
		"per_page": {"100"},
	}
	var api = "/issues/events"
	if issue > 0 {
		api = fmt.Sprintf("/issues/%d/events", issue)
	}
	urlStr := "https://api.github.com/repos/" + proj.Name + api + "?" + values.Encode()

	var (
		firstID   int64
		firstETag string
		lastID    int64
		stopped   bool
	)

	b := c.db.Batch()
	defer b.Apply()

Pages:
	for pg, err := range c.pages(ctx, urlStr, proj.EventETag) {
		if err == errNotModified {
			return nil
		}
		if err != nil {
			return err
		}

		for _, raw := range pg.body {
			var meta struct {
				ID    int64  `json:"id"`
				URL   string `json:"url"`
				Issue struct {
					Number int64 `json:"number"`
				} `json:"issue"`
			}
			if err := json.Unmarshal(raw, &meta); err != nil {
				return fmt.Errorf("parsing JSON: %v", err)
			}
			if issue > 0 {
				meta.Issue.Number = issue
			} else if meta.Issue.Number == 0 {
				// Locking a GitHub comment thread on a *commit*
				// appears in the /issues/events stream. For example
				// https://github.com/golang/go/commit/af43932c20d5b59cdffca45406754dbccbb46dfa
				// is now locked and has this GitHub event in the /issues/events stream:
				//
				//	{
				//		"id": 13683643830,
				//		"node_id": "LOE_lADOAWBuf8DPAAAAAy-b1bY",
				//		"url": "https://api.github.com/repos/golang/go/issues/events/13683643830",
				//		"actor": {
				//			"login": "ianlancetaylor",
				//			...
				//		},
				//		"event": "locked",
				//		"commit_id": "af43932c20d5b59cdffca45406754dbccbb46dfa",
				//		"commit_url": "https://api.github.com/repos/golang/go/commits/af43932c20d5b59cdffca45406754dbccbb46dfa",
				//		"created_at": "2024-07-29T16:56:44Z",
				//		"lock_reason": null,
				//		"issue": null,
				//		"performed_via_github_app": null
				//	}
				//
				// Assume the lack of issue number is something like that.
				c.slog.Warn("github sync event without issue id", "json", raw)
				continue
			}
			if meta.ID == 0 {
				return fmt.Errorf("parsing message: no id: %s", string(raw))
			}
			if firstID == 0 {
				firstID = meta.ID
				firstETag = pg.resp.Header.Get("Etag")
			}
			lastID = meta.ID
			if issue == 0 && (onlySetLatest || proj.EventID != 0 && meta.ID <= proj.EventID) {
				stopped = true
				break Pages
			}

			c.writeEvent(b, proj.Name, meta.Issue.Number, "/issues/events", meta.ID, raw)
			b.MaybeApply()
		}
	}

	if issue == 0 && lastID != 0 && !stopped {
		return fmt.Errorf("lost sync: missing event IDs between %d and %d", proj.EventID, lastID)
	}

	if issue == 0 {
		proj.EventID = firstID
		proj.EventETag = firstETag
		proj.store(c.db)
	}
	return nil
}

// writeEvent writes a single event to the database using SetTimed, to maintain a time-ordered index.
func (c *Client) writeEvent(b storage.Batch, project string, issue int64, api string, id int64, raw json.RawMessage) {
	timed.Set(c.db, b, eventKind, o(project, issue, api, id), o(ordered.Raw(raw)))
}

// errNotModified is returned by get when an etag is being used
// and the server returns a 304 not modified response.
var errNotModified = errors.New("304 not modified")

// get fetches url and decodes the body as JSON into obj.
//
// If etag is non-empty, the request includes an If-None-Match: etag header
// and get returns errNotModified if the server says the object is unmodified
// since that etag.
//
// get uses the api.github.com secret if available.
// Otherwise it makes an unauthenticated request.
func (c *Client) get(ctx context.Context, url, etag string, obj any) (*http.Response, error) {
	if c.divertEdits() {
		c.testMu.Lock()
		js := c.testEvents[url]
		c.testMu.Unlock()
		if js != nil {
			resp := &http.Response{
				StatusCode: 200,
				Status:     "200 OK",
			}
			return resp, json.Unmarshal(js, obj)
		}
	}

	auth := Token(c.secret)
	nrate := 0
	nfail := 0
Redo:
	c.slog.Info("github get", "url", url)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+auth)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("reading body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotModified { // 304
			return nil, errNotModified
		}
		if c.rateLimit(resp) {
			if nrate++; nrate > 20 {
				return nil, fmt.Errorf("%s # too many rate limits\n%s", resp.Status, data)
			}
			goto Redo
		}
		if resp.StatusCode == http.StatusInternalServerError || resp.StatusCode == http.StatusBadGateway { // 500, 502
			c.slog.Error("github get server failure", "code", resp.StatusCode, "status", resp.Status, "body", string(data))
			if nfail++; nfail < 3 {
				time.Sleep(time.Duration(nfail) * 2 * time.Second)
				goto Redo
			}
		}
		return nil, fmt.Errorf("%s\n%s", resp.Status, data)
	}
	return resp, json.Unmarshal(data, obj)
}

// Token returns the secret for "api.github.com".
func Token(sdb secret.DB) string {
	auth, _ := sdb.Get("api.github.com")
	if _, pass, ok := strings.Cut(auth, ":"); ok {
		// Accept token as "password" in user:pass from netrc secret store
		return pass
	}
	return auth
}

// A page is an HTTP response with a body that is a JSON array of objects.
// The objects are not decoded (they are json.RawMessages).
type page struct {
	resp *http.Response
	body []json.RawMessage
}

// pages returns a paginated result starting at url and using etag.
// If pages encounters an error, it yields nil, err.
func (c *Client) pages(ctx context.Context, url, etag string) iter.Seq2[*page, error] {
	return func(yield func(*page, error) bool) {
		for url != "" {
			var body []json.RawMessage
			resp, err := c.get(ctx, url, etag, &body)
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(&page{resp, body}, nil) {
				return
			}
			url = findNext(resp.Header.Get("Link"))
		}
	}
}

// findNext finds the "next" URL in the Link header value.
// Sample Link header:
//
//	Link: <https://api.github.com/repositories/26468385/issues?direction=asc&page=2&per_page=100&sort=updated&state=all>; rel="next", <https://api.github.com/repositories/26468385/issues?direction=asc&page=2&per_page=100&sort=updated&state=all>; rel="last"
//
// The format is a comma-separated list of <LINK>; rel="type".
func findNext(link string) string {
	for link != "" {
		link = strings.TrimSpace(link)
		if !strings.HasPrefix(link, "<") {
			break
		}
		i := strings.Index(link, ">")
		if i < 0 {
			break
		}
		linkURL := link[1:i]
		link = strings.TrimSpace(link[i+1:])
		for strings.HasPrefix(link, ";") {
			link = strings.TrimSpace(link[1:])
			i := strings.Index(link, ";")
			j := strings.Index(link, ",")
			if i < 0 || j >= 0 && j < i {
				i = j
			}
			if i < 0 {
				i = len(link)
			}
			attr := strings.TrimSpace(link[:i])
			if attr == `rel="next"` {
				return linkURL
			}
			link = link[i:]
		}
		if !strings.HasPrefix(link, ",") {
			break
		}
		link = strings.TrimSpace(link[1:])
	}
	return ""
}

// rateLimit looks at the response to decide whether a rate limit has been applied.
// If so, rateLimit sleeps until the time specified in the response, plus a bit extra.
// rateLimit reports whether this was a rate-limit response.
func (c *Client) rateLimit(resp *http.Response) bool {
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("X-Ratelimit-Remaining") != "0" {
		return false
	}
	n, _ := strconv.Atoi(resp.Header.Get("X-Ratelimit-Reset"))
	if n == 0 {
		return false
	}
	t := time.Unix(int64(n), 0)
	now := time.Now()

	const timeSkew = 1 * time.Minute
	delay := t.Sub(now) + timeSkew
	if delay < 0 {
		// We are being asked to sleep until a time that already happened.
		// If it happened recently (within timeSkew), then maybe our clock
		// is just out of sync with GitHub's clock.
		// But if it happened long ago, we are probably reading an old HTTP trace.
		// In that case, return false (we didn't find a valid ratelimit).
		return false
	}
	c.slog.Info("github ratelimit", "reset", t.Format(time.RFC3339),
		"limit", resp.Header.Get("X-Ratelimit-Limit"),
		"remaining", resp.Header.Get("X-Ratelimit-Remaining"),
		"used", resp.Header.Get("X-Ratelimit-Used"))
	time.Sleep(delay)
	return true
}
