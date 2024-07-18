// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package related implements posting about related issues to GitHub.
package related

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/github"
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
	return &Poster{
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

// deletePosted deletes all the “posted on this issue” notes.
func (p *Poster) deletePosted() {
	p.db.DeleteRange(ordered.Encode("triage.Posted"), ordered.Encode("triage.Posted", ordered.Inf))
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
// If [Poster.EnablePosts] has been called, then [Run] also posts the comment to GitHub,
// records in the database that it has posted to GitHub to make sure it never posts to that issue again,
// and advances its GitHub issue watcher's incremental cursor to speed future calls to [Run].
//
// When [Poster.EnablePosts] has not been called, Run only logs the comments it would post.
// Future calls to Run will reprocess the same issues and re-log the same comments.
func (p *Poster) Run(ctx context.Context) {
	p.slog.Info("related.Poster start", "name", p.name)
	defer p.slog.Info("related.Poster end", "name", p.name)

	defer p.watcher.Flush()

Watcher:
	for e := range p.watcher.Recent() {
		if !p.projects[e.Project] || e.API != "/issues" {
			continue
		}
		issue := e.Typed.(*github.Issue)
		if issue.State == "closed" || issue.PullRequest != nil {
			continue
		}
		tm, err := time.Parse(time.RFC3339, issue.CreatedAt)
		if err != nil {
			p.slog.Error("triage parse createdat", "CreatedAt", issue.CreatedAt, "err", err)
			continue
		}
		if tm.Before(p.timeLimit) {
			continue
		}
		for _, ig := range p.ignores {
			if ig(issue) {
				continue Watcher
			}
		}

		// TODO: Perhaps this key should include p.name, but perhaps not.
		// This makes sure we only ever post to each issue once.
		posted := ordered.Encode("triage.Posted", e.Project, e.Issue)
		if _, ok := p.db.Get(posted); ok {
			continue
		}

		u := fmt.Sprintf("https://github.com/%s/issues/%d", e.Project, e.Issue)
		p.slog.Debug("triage client consider", "url", u)
		vec, ok := p.vdb.Get(u)
		if !ok {
			p.slog.Error("triage lookup failed", "url", u)
			continue
		}
		results := p.vdb.Search(vec, p.maxResults+5)
		if len(results) > 0 && results[0].ID == u {
			results = results[1:]
		}
		for i, r := range results {
			if r.Score < p.scoreCutoff {
				results = results[:i]
				break
			}
		}
		if len(results) > p.maxResults {
			results = results[:p.maxResults]
		}
		if len(results) == 0 {
			if p.post {
				p.watcher.MarkOld(e.DBTime)
			}
			continue
		}
		var comment strings.Builder
		fmt.Fprintf(&comment, "**Related Issues**\n\n")
		for _, r := range results {
			title := r.ID
			if d, ok := p.docs.Get(r.ID); ok {
				title = d.Title
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

		p.slog.Info("related.Poster post", "name", p.name, "project", e.Project, "issue", e.Issue, "comment", comment.String())

		if !p.post {
			continue
		}

		if err := p.github.PostIssueComment(ctx, issue, &github.IssueCommentChanges{Body: comment.String()}); err != nil {
			p.slog.Error("PostIssueComment", "issue", e.Issue, "err", err)
			continue
		}
		p.db.Set(posted, nil)
		p.watcher.MarkOld(e.DBTime)

		// Flush immediately to make sure we don't re-post if interrupted later in the loop.
		p.watcher.Flush()
		p.db.Flush()
	}
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
