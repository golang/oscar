// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"encoding/json"
	"fmt"
	"iter"
	"math"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/model"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

// LookupIssueURL looks up an issue by URL
// (for example "https://github.com/golang/go/issues/12345"),
// only consulting the database (not actual GitHub).
func (c *Client) LookupIssueURL(url string) (*Issue, error) {
	bad := func() (*Issue, error) {
		return nil, fmt.Errorf("not a github URL: %q", url)
	}
	proj, ok := strings.CutPrefix(url, "https://github.com/")
	if !ok {
		return bad()
	}
	// If this used strings.Index instead of strings.LastIndex,
	// we could use strings.Cut instead. But it doesn't, so we can't.
	// Have to handle a hypothetical repo named golang/issues,
	// which would have URLs like https://github.com/golang/issues/issues/12345.
	i := strings.LastIndex(proj, "/issues/")
	if i < 0 {
		return bad()
	}
	proj, num := proj[:i], proj[i+len("/issues/"):]
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n <= 0 {
		return bad()
	}

	return LookupIssue(c.db, proj, n)
}

// LookupIssue looks up an issue by project and issue number
// (for example "golang/go", 12345), only consulting the database
// (not actual GitHub).
func LookupIssue(db storage.DB, project string, issue int64) (*Issue, error) {
	for e := range eventsByAPI(db, project, issue, "/issues") {
		return e.Typed.(*Issue), nil
	}
	return nil, fmt.Errorf("github.LookupIssue: issue %s#%d not in database", project, issue)
}

// LookupIssues returns an iterator over issues between issueMin and issueMax,
// only consulting the database (not actual GitHub).
func LookupIssues(db storage.DB, project string, issueMin, issueMax int64) iter.Seq[*Issue] {
	return func(yield func(*Issue) bool) {
		for e := range events(db, project, issueMin, issueMax) {
			if e.API == "/issues" {
				if !yield(e.Typed.(*Issue)) {
					break
				}
			}
		}
	}
}

// An Event is a single GitHub issue event stored in the database.
type Event struct {
	DBTime  timed.DBTime // when event was last written
	Project string       // project ("golang/go")
	Issue   int64        // issue number
	API     string       // API endpoint for event: "/issues", "/issues/comments", or "/issues/events"
	ID      int64        // ID of event; each API has a different ID space. (Project, Issue, API, ID) is assumed unique
	JSON    []byte       // JSON for the event data
	Typed   any          // Typed unmarshaling of the event data, of type *Issue, *IssueComment, or *IssueEvent
}

var _ docs.Entry = (*Event)(nil)

// LastWritten implements [docs.Entry.LastWritten].
func (e *Event) LastWritten() timed.DBTime {
	return e.DBTime
}

// CleanTitle should clean the title for indexing.
// For now we assume the LLM is good enough at Markdown not to bother.
func CleanTitle(title string) string {
	// TODO
	return title
}

// CleanBody should clean the body for indexing.
// For now we assume the LLM is good enough at Markdown not to bother.
// In the future we may want to make various changes like inlining
// the programs associated with playground URLs,
// and we may also want to remove any HTML tags from the Markdown.
func CleanBody(body string) string {
	// TODO
	return body
}

// Events returns an iterator over issue events for the given project,
// limited to issues in the range issueMin ≤ issue ≤ issueMax.
// If issueMax < 0, there is no upper limit.
// The events are iterated over in (Project, Issue, API, ID) order,
// so "/issues" events come first, then "/issues/comments", then "/issues/events".
// Within a specific API, the events are ordered by increasing ID,
// which corresponds to increasing event time on GitHub.
func (c *Client) Events(project string, issueMin, issueMax int64) iter.Seq[*Event] {
	return events(c.db, project, issueMin, issueMax)
}

func events(db storage.DB, project string, issueMin, issueMax int64) iter.Seq[*Event] {
	return func(yield func(*Event) bool) {
		start := o(project, issueMin)
		if issueMax < 0 {
			issueMax = math.MaxInt64
		}
		end := o(project, issueMax, ordered.Inf)
		for t := range timed.Scan(db, eventKind, start, end) {
			if !yield(decodeEvent(db, t)) {
				return
			}
		}
	}
}

// EventsAfter returns an iterator over events in the given project after DBTime t,
// which should be e.DBTime from the most recent processed event.
// The events are iterated over in DBTime order, so the DBTime of the last
// successfully processed event can be used in a future call to EventsAfter.
// If project is the empty string, then events from all projects are returned.
func (c *Client) EventsAfter(t timed.DBTime, project string) iter.Seq[*Event] {
	filter := func(key []byte) bool {
		if project == "" {
			return true
		}
		var p string
		if _, err := ordered.DecodePrefix(key, &p); err != nil {
			c.db.Panic("github EventsAfter decode", "key", storage.Fmt(key), "err", err)
		}
		return p == project
	}

	return func(yield func(*Event) bool) {
		for e := range timed.ScanAfter(c.slog, c.db, eventKind, t, filter) {
			if !yield(decodeEvent(c.db, e)) {
				return
			}
		}
	}
}

// decodeEvent decodes the key, val pair into an Event.
// It calls db.Panic for malformed data.
func decodeEvent(db storage.DB, t *timed.Entry) *Event {
	var e Event
	e.DBTime = t.ModTime
	if err := ordered.Decode(t.Key, &e.Project, &e.Issue, &e.API, &e.ID); err != nil {
		db.Panic("github event decode", "key", storage.Fmt(t.Key), "err", err)
	}

	var js ordered.Raw
	if err := ordered.Decode(t.Val, &js); err != nil {
		db.Panic("github event val decode", "key", storage.Fmt(t.Key), "val", storage.Fmt(t.Val), "err", err)
	}
	e.JSON = js
	switch e.API {
	default:
		db.Panic("github event invalid API", "api", e.API)
	case "/issues":
		e.Typed = new(Issue)
	case "/issues/comments":
		e.Typed = new(IssueComment)
	case "/issues/events":
		e.Typed = new(IssueEvent)
	}
	if err := json.Unmarshal(js, e.Typed); err != nil {
		db.Panic("github event json", "js", string(js), "err", err)
	}
	return &e
}

// EventWatcher returns a new [timed.Watcher] with the given name.
// It picks up where any previous Watcher of the same name left off.
func (c *Client) EventWatcher(name string) *timed.Watcher[*Event] {
	decode := func(t *timed.Entry) *Event {
		return decodeEvent(c.db, t)
	}
	return timed.NewWatcher(c.slog, c.db, name, eventKind, decode)
}

// IssueEvent is the GitHub JSON structure for an issue metadata event.
type IssueEvent struct {
	// NOTE: Issue field is not present when downloading for a specific issue,
	// only in the master feed for the whole repo. So do not add it here.
	ID         int64     `json:"id"`
	URL        string    `json:"url"`
	Actor      User      `json:"actor"`
	Event      string    `json:"event"`
	Labels     []Label   `json:"labels"`
	LockReason string    `json:"lock_reason"`
	CreatedAt  string    `json:"created_at"`
	CommitID   string    `json:"commit_id"`
	Assigner   User      `json:"assigner"`
	Assignees  []User    `json:"assignees"`
	Milestone  Milestone `json:"milestone"`
	Rename     Rename    `json:"rename"`
}

// A User represents a user or organization account in GitHub JSON.
type User struct {
	Login string `json:"login"`
}

// A Label represents a project issue tracker label in GitHub JSON.
type Label struct {
	Name string `json:"name"`
}

// A Milestone represents a project issue milestone in GitHub JSON.
type Milestone struct {
	Title string `json:"title"`
}

// A Rename describes an issue title renaming in GitHub JSON.
type Rename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ParseIssueURL expects a GitHUB API URL for an issue (for example "https://api.github.com/repos/org/r/issues/123")
// and returns the project (for example "org/r") and issue number.
func ParseIssueURL(u string) (project string, number int64, err error) {
	bad := func() error { return fmt.Errorf("not a GitHub isue URL: %q", u) }
	project = urlToProject(u)
	if project == "" {
		return "", 0, bad()
	}
	number = baseToInt64(u)
	if number == 0 {
		return "", 0, bad()
	}
	return project, number, nil
}

func urlToProject(u string) string {
	u, ok := strings.CutPrefix(u, "https://api.github.com/repos/")
	if !ok {
		return ""
	}
	i := strings.Index(u, "/")
	if i < 0 {
		return ""
	}
	j := strings.Index(u[i+1:], "/")
	if j < 0 {
		return ""
	}
	return u[:i+1+j]
}

func baseToInt64(u string) int64 {
	i, err := strconv.ParseInt(u[strings.LastIndex(u, "/")+1:], 10, 64)
	if i <= 0 || err != nil {
		return 0
	}
	return i
}

// IssueComment is the GitHub JSON structure for an issue comment event.
type IssueComment struct {
	URL       string `json:"url"`
	IssueURL  string `json:"issue_url"`
	HTMLURL   string `json:"html_url"`
	User      User   `json:"user"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Body      string `json:"body"`
}

// Project returns the issue comment's GitHub project (for example, "golang/go").
func (x *IssueComment) Project() string {
	return urlToProject(x.URL)
}

// Issue returns the issue comment's issue number.
func (x *IssueComment) Issue() int64 {
	u, _, _ := strings.Cut(x.HTMLURL, "#")
	return baseToInt64(u)
}

// CommentID returns the issue comment's numeric ID.
// The ID appears to be unique across all comments on GitHub,
// but we only assume it is unique within a single issue.
func (x *IssueComment) CommentID() int64 {
	return baseToInt64(x.URL)
}

// Methods implementing model.Post.
func (x *IssueComment) ID() string                 { return x.URL }
func (x *IssueComment) Title_() string             { return "" }
func (x *IssueComment) Body_() string              { return x.Body }
func (x *IssueComment) CreatedAt_() time.Time      { return mustParseTime(x.CreatedAt) }
func (x *IssueComment) UpdatedAt_() time.Time      { return mustParseTime(x.UpdatedAt) }
func (x *IssueComment) Author() *model.Identity    { panic("TODO: convert User to Identity") }
func (x *IssueComment) CanEdit() bool              { return true }
func (x *IssueComment) CanHaveChildren() bool      { return false }
func (x *IssueComment) ParentID() string           { return x.IssueURL }
func (x *IssueComment) Properties() map[string]any { return nil } //TODO: add other fields

func (x *IssueComment) Updates() model.PostUpdates {
	return &IssueCommentChanges{}
}

var _ model.Post = (*IssueComment)(nil)

// Issue is the GitHub JSON structure for an issue creation event.
type Issue struct {
	URL              string    `json:"url"`
	HTMLURL          string    `json:"html_url"`
	Number           int64     `json:"number"`
	User             User      `json:"user"`
	Title            string    `json:"title"`
	CreatedAt        string    `json:"created_at"`
	UpdatedAt        string    `json:"updated_at"`
	ClosedAt         string    `json:"closed_at"`
	Body             string    `json:"body"`
	Assignees        []User    `json:"assignees"`
	Milestone        Milestone `json:"milestone"`
	State            string    `json:"state"`
	PullRequest      *struct{} `json:"pull_request"`
	Locked           bool      `json:"locked"`
	ActiveLockReason string    `json:"active_lock_reason"`
	Labels           []Label   `json:"labels"`
}

// Project returns the issue's GitHub project (for example, "golang/go").
func (x *Issue) Project() string {
	return urlToProject(x.URL)
}

// DocID returns the ID of this issue for storage in a docs.Corpus
// or a storage.VectorDB.
func (i *Issue) DocID() string {
	return i.HTMLURL
}

// Methods implementing model.Post.
func (x *Issue) ID() string                 { return x.URL }
func (x *Issue) Title_() string             { return x.Title }
func (x *Issue) Body_() string              { return x.Body }
func (x *Issue) CreatedAt_() time.Time      { return mustParseTime(x.CreatedAt) }
func (x *Issue) UpdatedAt_() time.Time      { return mustParseTime(x.UpdatedAt) }
func (x *Issue) Author() *model.Identity    { panic("TODO: convert User to Identity") }
func (x *Issue) CanEdit() bool              { return true }
func (x *Issue) CanHaveChildren() bool      { return false }
func (x *Issue) ParentID() string           { return "" }
func (x *Issue) Properties() map[string]any { return nil } //TODO: add other fields

func (x *Issue) Updates() model.PostUpdates { return &IssueChanges{} }

var _ model.Post = (*Issue)(nil)

func mustParseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		storage.Panic("bad time: %v", err)
	}
	return t
}
