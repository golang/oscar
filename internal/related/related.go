// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package related implements posting about related issues to GitHub.
package related

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/search"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

// A Poster posts to GitHub about related issues (and eventually other documents).
type Poster struct {
	slog        *slog.Logger
	db          storage.DB
	vdb         storage.VectorDB
	github      *github.Client
	docs        *docs.Corpus
	projects    map[string]bool
	watcher     *timed.Watcher[*github.Event]
	name        string
	timeLimit   time.Time
	ignores     []func(*github.Issue) bool
	maxResults  int
	scoreCutoff float64
	post        bool
	// For the action log.
	actionKind string
	logAction  actions.BeforeFunc
}

// New creates and returns a new Poster. It logs to lg, stores state in db,
// watches for new GitHub issues using gh, looks up related documents in vdb,
// and reads the document content from docs.
// For the purposes of storing its own state, it uses the given name.
// Future calls to New with the same name will use the same state.
//
// Use the [Poster] methods to configure the posting parameters
// (especially [Poster.EnableProject] and [Poster.EnablePosts])
// before calling [Poster.Run].
func New(lg *slog.Logger, db storage.DB, gh *github.Client, vdb storage.VectorDB, docs *docs.Corpus, name string) *Poster {
	p := &Poster{
		slog:        lg,
		db:          db,
		vdb:         vdb,
		github:      gh,
		docs:        docs,
		projects:    make(map[string]bool),
		watcher:     gh.EventWatcher("related.Poster:" + name),
		name:        name,
		timeLimit:   time.Now().Add(-defaultTooOld),
		maxResults:  defaultMaxResults,
		scoreCutoff: defaultScoreCutoff,
	}
	// TODO: Perhaps the action kind should include name, but perhaps not.
	// This makes sure we only ever post to each issue once.
	p.actionKind = "related.Poster"
	p.logAction = actions.Register(p.actionKind, &actioner{p})
	return p
}

// SetTimeLimit controls how old an issue can be for the Poster to post to it.
// Issues created before time t will be skipped.
// The default is not to post to issues that are more than 48 hours old
// at the time of the call to [New].
func (p *Poster) SetTimeLimit(t time.Time) {
	p.timeLimit = t
}

const defaultTooOld = 48 * time.Hour

// SetMaxResults sets the maximum number of related documents to
// post to the issue.
// The default is 10.
func (p *Poster) SetMaxResults(max int) {
	p.maxResults = max
}

const defaultMaxResults = 10

// SetMinScore sets the minimum vector search score that a
// [storage.VectorResult] must have to be considered a related document
// The default is 0.82, which was determined empirically.
func (p *Poster) SetMinScore(min float64) {
	p.scoreCutoff = min
}

const defaultScoreCutoff = 0.82

// SkipBodyContains configures the Poster to skip issues with a body containing
// the given text.
func (p *Poster) SkipBodyContains(text string) {
	p.ignores = append(p.ignores, func(issue *github.Issue) bool {
		return strings.Contains(issue.Body, text)
	})
}

// SkipTitlePrefix configures the Poster to skip issues with a title starting
// with the given prefix.
func (p *Poster) SkipTitlePrefix(prefix string) {
	p.ignores = append(p.ignores, func(issue *github.Issue) bool {
		return strings.HasPrefix(issue.Title, prefix)
	})
}

// SkipTitleSuffix configures the Poster to skip issues with a title starting
// with the given suffix.
func (p *Poster) SkipTitleSuffix(suffix string) {
	p.ignores = append(p.ignores, func(issue *github.Issue) bool {
		return strings.HasSuffix(issue.Title, suffix)
	})
}

// EnableProject enables the Poster to post on issues in the given GitHub project (for example "golang/go").
// See also [Poster.EnablePosts], which must also be called to post anything to GitHub.
func (p *Poster) EnableProject(project string) {
	p.projects[project] = true
}

// EnablePosts enables the Poster to post to GitHub.
// If EnablePosts has not been called, [Poster.Run] logs what it would post but does not post the messages.
// See also [Poster.EnableProject], which must also be called to set the projects being considered.
func (p *Poster) EnablePosts() {
	p.post = true
}

// An action has all the information needed to post a comment to a GitHub issue.
type action struct {
	Issue   *github.Issue
	Changes *github.IssueCommentChanges
}

// result is the result of apply an action.
type result struct {
	URL string // URL of new comment
}

// Run runs a single round of posting to GitHub.
// It scans all open issues that have been created since the last call to [Poster.Run]
// using a Poster with the same name (see [New]).
// Run skips closed issues, and it also skips pull requests.
//
// For each issue that matches the configured posting constraints
// (see [Poster.EnableProject], [Poster.SetTimeLimit], [Poster.IgnoreBodyContains], [Poster.IgnoreTitlePrefix], and [Poster.IgnoreTitleSuffix]),
// Run computes an embedding of the issue body text (ignoring comments)
// and looks in the vector database for other documents (currently only issues)
// that are aligned closely enough with that body text
// (see [Poster.SetMinScore]) and posts a limited number of matches
// (see [Poster.SetMaxResults]).
//
// Run logs each post to the [slog.Logger] passed to [New].
// If [Poster.EnablePosts] has been called, then [Run] also adds an action to the
// action log an action that will post the comment to GitHub (see [actions.Run]),
// and advances its GitHub issue watcher's incremental cursor to speed future calls to [Run].
//
// When [Poster.EnablePosts] has not been called, Run only logs the comments it would post.
// Future calls to Run will reprocess the same issues and re-log the same comments.
func (p *Poster) Run(ctx context.Context) error {
	p.slog.Info("related.Poster start", "name", p.name, "post", p.post, "latest", p.watcher.Latest())
	defer func() {
		p.slog.Info("related.Poster end", "name", p.name, "latest", p.watcher.Latest())
	}()

	defer p.watcher.Flush()
	for e := range p.watcher.Recent() {
		advance, err := p.logPostIssue(ctx, e)
		if err != nil {
			p.slog.Error("related.Poster", "issue", e.Issue, "event", e, "error", err)
			continue
		}
		if advance {
			p.watcher.MarkOld(e.DBTime)
			// Flush immediately to make sure we don't re-post if interrupted later in the loop.
			p.watcher.Flush()
			p.slog.Info("related.Poster advanced watcher", "latest", p.watcher.Latest(), "event", e)
		} else {
			p.slog.Info("related.Poster watcher not advanced", "latest", p.watcher.Latest(), "event", e)
		}
	}
	return nil
}

// Post posts an issue comment for the given GitHub issue.
//
// It follows the same logic as [Poster.Run] for a single event, except
// that it does not rely on or modify the Poster's GitHub issue watcher's
// incremental cursor.
// This means that [Poster.Post] can be called on any issue event without
// affecting the starting point of future calls to [Poster.Run].
//
// It requires that there be a database and vector database entry for
// the given issue.
func (p *Poster) Post(ctx context.Context, project string, issue int64) error {
	e := lookupIssueEvent(project, issue, p.github)
	if e == nil {
		return fmt.Errorf("related.Poster.Post(project=%s, issue=%d): %w", project, issue, errEventNotFound)
	}
	_, err := p.logPostIssue(ctx, e)
	return err
}

var (
	errEventNotFound          = errors.New("event not found in database")
	errVectorSearchFailed     = errors.New("vector search failed")
	errPostIssueCommentFailed = errors.New("post issue comment failed")
)

// lookupIssueEvent returns the first event for the "/issues" API with
// the given ID in the database, or nil if not found.
func lookupIssueEvent(project string, issue int64, gh *github.Client) *github.Event {
	for event := range gh.Events(project, issue, issue) {
		if event.API == "/issues" {
			return event
		}
	}
	return nil
}

// logPostIssue logs an action to post an issue for the event.
// advance is true if the event should be considered to have been
// handled by this or a previous run function, indicating
// that the Poster's watcher can be advanced.
// An issue is handled if
//   - posting is enabled, AND
//   - an issue posting was successfully logged, or no issue was needed
//     because no related documents were found
//
// Skipped issues are not considered handled.
func (p *Poster) logPostIssue(ctx context.Context, e *github.Event) (advance bool, _ error) {
	if skip, reason := p.skip(e); skip {
		p.slog.Info("related.Poster skip", "name", p.name, "project",
			e.Project, "issue", e.Issue, "reason", reason, "event", e)
		return false, nil
	}

	// If an action has already been logged for this event, do nothing.
	// This is just an optimization to avoid an expensive vector search, so we don't
	// need a lock. [actions.before] will lock to avoid multiple log entries.
	if _, ok := actions.Get(p.db, p.actionKind, logKey(e)); ok {
		p.slog.Info("related.Poster already logged", "name", p.name, "project", e.Project, "issue", e.Issue, "event", e)
		// If posting is enabled, we can advance the watcher because
		// a comment has already been logged for this issue.
		return p.post, nil
	}

	u := issueURL(e.Project, e.Issue)
	p.slog.Debug("related.Poster consider", "url", u)
	results, ok := p.search(u)
	if !ok {
		return false, fmt.Errorf("%w url=%s", errVectorSearchFailed, u)
	}
	if len(results) == 0 {
		p.slog.Info("related.Poster found no related documents", "name", p.name, "project", e.Project, "issue", e.Issue, "event", e)
		// If posting is enabled, an issue with no related documents
		// should be considered handled, and not looked at again.
		return p.post, nil
	}
	comment := p.comment(results)
	p.slog.Info("related.Poster post", "name", p.name, "project", e.Project, "issue", e.Issue, "comment", comment)

	if !p.post {
		// Posting is disabled so we did not handle this issue.
		return false, nil
	}

	act := &action{
		Issue:   e.Typed.(*github.Issue),
		Changes: &github.IssueCommentChanges{Body: comment},
	}
	p.logAction(p.db, logKey(e), storage.JSON(act), !actions.RequiresApproval)
	return true, nil
}

type actioner struct {
	p *Poster
}

func (ar *actioner) Run(ctx context.Context, data []byte) ([]byte, error) {
	return ar.p.runFromActionLog(ctx, data)
}

func (ar *actioner) ForDisplay(data []byte) string {
	var a action
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}
	return a.Issue.HTMLURL + "\n" + a.Changes.Body
}

// runFromActionLog is called by actions.Run to execute an action.
// It decodes the action, calls [Poster.runAction], then encodes the result.
func (p *Poster) runFromActionLog(ctx context.Context, data []byte) ([]byte, error) {
	var a action
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	res, err := p.runAction(ctx, &a)
	if err != nil {
		return nil, err
	}
	return storage.JSON(res), nil
}

// runAction runs the given action.
func (p *Poster) runAction(ctx context.Context, a *action) (*result, error) {
	url, err := p.github.PostIssueComment(ctx, a.Issue, a.Changes)
	// If GitHub returns an error, add it to the action log for this action.
	//
	// Gaby's original behavior was to log the error, not advance the watcher,
	// and continue iterating over watcher.Recent. So subsequent successful
	// posts would advance the watcher over the failed one, leaving only the
	// slog entry as evidence of the failure.
	//
	// The current behavior always advances the watcher and preserves the error
	// in the action log.
	//
	// It is unclear what the right behavior is, but at least at present all
	// failed actions are available to the program and could be re-run.
	if err != nil {
		return nil, fmt.Errorf("%w issue=%d: %v", errPostIssueCommentFailed, a.Issue.Number, err)
	}

	return &result{URL: url}, nil
}

// issueURL returns the URL of the GitHub issue in the given project.
func issueURL(project string, issue int64) string {
	return fmt.Sprintf("https://github.com/%s/issues/%d", project, issue)
}

// search performs a vector search to find related issues for the given
// issue URL. It removes any results that don't meet the cutoff in
// p.scoreCutoff and trims the results list to a max length of p.maxResults.
// It expects that there is already an entry for the url in the vector
// database, and returns ok=false if there is no such entry.
func (p *Poster) search(u string) (_ []search.Result, ok bool) {
	vec, ok := p.vdb.Get(u)
	if !ok {
		return nil, false
	}
	results := search.Vector(p.vdb, p.docs, &search.VectorRequest{
		Options: search.Options{
			Threshold: p.scoreCutoff,
			Limit:     p.maxResults + 5, // add a buffer for filters
			DenyKind:  []string{search.KindUnknown},
		},
		Vector: vec,
	})
	// Remove the query itself if present.
	if len(results) > 0 && results[0].ID == u {
		results = results[1:]
	}
	// Trim length.
	if len(results) > p.maxResults {
		results = results[:p.maxResults]
	}
	return results, true
}

// comment returns the comment to post to GitHub for the given related
// issues.
func (p *Poster) comment(results []search.Result) string {
	var comment strings.Builder
	fmt.Fprintf(&comment, "**Related Issues and Documentation**\n\n")
	for _, r := range results {
		title := r.ID
		if r.Title != "" {
			title = r.Title
		}
		info := ""
		if issue, err := p.github.LookupIssueURL(r.ID); err == nil {
			info = fmt.Sprint(" #", issue.Number)
			if issue.ClosedAt != "" {
				info += " (closed)"
			}
		}
		fmt.Fprintf(&comment, " - [%s%s](%s) <!-- score=%.5f -->\n", markdownEscape(title), info, r.ID, r.Score)
	}
	fmt.Fprintf(&comment, "\n<sub>(Emoji vote if this was helpful or unhelpful; more detailed feedback welcome in [this discussion](https://github.com/golang/go/discussions/67901).)</sub>\n")
	return comment.String()
}

// skip reports whether the event should be skipped and why.
func (p *Poster) skip(e *github.Event) (_ bool, reason string) {
	if !p.projects[e.Project] {
		return true, fmt.Sprintf("project %s not enabled for this Poster", e.Project)
	}
	if e.API != "/issues" {
		return true, fmt.Sprintf("wrong API %s (expected %s)", e.API, "/issues")
	}
	issue := e.Typed.(*github.Issue)
	if issue.State == "closed" {
		return true, "issue is closed"
	}
	if issue.PullRequest != nil {
		return true, "pull request"
	}
	tm, err := time.Parse(time.RFC3339, issue.CreatedAt)
	if err != nil {
		p.slog.Error("related.Poster parse createdat", "CreatedAt", issue.CreatedAt, "err", err)
		return true, "could not parse createdat"
	}
	if tm.Before(p.timeLimit) {
		return true, fmt.Sprintf("created=%s before time limit=%s", tm, p.timeLimit)
	}
	for i, ig := range p.ignores {
		if ig(issue) {
			return true, fmt.Sprintf("ignored by function ignores[%d]", i)
		}
	}
	if p.posted(e) {
		return true, "already posted"
	}
	return false, ""
}

// posted reports whether the event has already been posted.
// This should only be necessary for a short time, since the action log
// is now handling this check.
func (p *Poster) posted(e *github.Event) bool {
	_, ok := p.db.Get(postedKey(e))
	return ok
}

// postedKey returns the database key to use when marking an event as posted.
func postedKey(e *github.Event) []byte {
	return ordered.Encode("triage.Posted", e.Project, e.Issue)
}

// logKey returns the key for the event in the action log.
// This is only a portion of the database key; it is prefixed by the Poster's action
// kind.
func logKey(e *github.Event) []byte {
	return ordered.Encode(e.Project, e.Issue)
}

// Latest returns the latest known DBTime marked old by the Poster's Watcher.
func (p *Poster) Latest() timed.DBTime {
	return p.watcher.Latest()
}

var markdownEscaper = strings.NewReplacer(
	"_", `\_`,
	"*", `\*`,
	"`", "\\`",
	"[", `\[`,
	"]", `\]`,
	"<", `\<`,
	">", `\>`,
	"&", `\&`,
)

func markdownEscape(s string) string {
	return markdownEscaper.Replace(s)
}
