// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package overview

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/github/wrap"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

// poster is the state needed to modify GitHub.
type poster struct {
	slog    *slog.Logger
	gh      *github.Client                // the GitHub client to use to read/write to GitHub
	db      storage.DB                    // the database to use to store state
	watcher *timed.Watcher[*github.Event] // a watcher over GitHub events, used to remember which events we have already processed

	minComments        int             // the minimum number of (non-skipped) comments an issue must have to get an overview (default: [defaultMinComments])
	maxIssueAge        time.Duration   // the maximum age (time since creation) of an issue to get an overview (default: [defaultMaxAge])
	skipIssueAuthors   map[string]bool // skip issues authored by these GitHub users (default: none)
	skipCommentAuthors map[string]bool // skip comments authored by these GitHub users when determining whether an issue meets the threshold to get an overview (default: none)

	name     string
	bot      string          // the login name of GitHub user that will post overviews, e.g. "gabyhelp"
	projects map[string]bool // the GitHub projects this poster will post to (default: none)

	w *wrap.Wrapper // used to wrap edits made to GitHub with tags. Allows the poster to identify its own edits

	// For the action log.
	requireApproval bool // whether to require approval for actions (default: true)
	logAction       actions.BeforeFunc

	// if true, attempt to find actions by the bot that are missing from the action log (using tags)
	findUnloggedActions bool

	// in-memory map used to speed up issueState lookups for issues
	// that have already been processed by a given call to [poster.run].
	// map keys are [poster.issueStateKey]s.
	// must only be modified inside [poster.run], while holding a lock
	// on runKey.
	runState map[string]*issueState
}

// newPoster returns a new Overviews poster.
func newPoster(lg *slog.Logger, db storage.DB, gh *github.Client, name, bot string) *poster {
	p := &poster{
		slog:            lg,
		name:            name,
		bot:             bot,
		gh:              gh,
		db:              db,
		watcher:         gh.EventWatcher(actionKind + name + bot),
		projects:        make(map[string]bool),
		minComments:     defaultMinComments,
		w:               wrap.New(bot, name),
		requireApproval: true,
		maxIssueAge:     defaultMaxAge,
	}
	p.logAction = actions.Register(actionKind, &actioner{p})
	return p
}

// run logs post or update actions to the action log for all issue events that need one.
// getOverview is the function to generate an overview of a GitHub issue.
// now is the current time (used to determine if an issue is too old).
//
// The general strategy is as follows:
//
//	For each new issue comment event in an enabled project:
//	- If a higher numbered issue comment for the issue has already been processed, skip the event.
//	- Otherwise, find the corresponding issue and all its comments.
//	- Check if the issue needs an overview (see [poster.skip]). If not, mark the issue processed
//	  at its highest numbered comment and continue to the next event.
//	- If so, log an action for the event (see [poster.logPostOrUpdate]) and mark the issue
//	  processed at its highest numbered comment.
//
// This strategy relies on consulting the database multiple times to find an issue
// and its comments. It assumes that the comments are not changing during its run.
// Therefore, this function must not be run in parallel with a GitHub sync.
//
// Note: Unlike some other functions in this repo, [poster.run] does not have
// a "dry run" mode which does not advance the watcher or create any database entries.
// This is because the algorithm depends on the presence of database entries to determine which
// issues have already been processed, so it is not clear how to create a dry run mode
// without introducing added complexity. The function can instead be tested without permanent
// side effects by using the memory overlay functionality of gaby.
//
// run is not intended to be used concurrently.
// a database lock (see [Client.runKey]) should be held to ensure calls to [poster.run]
// with the same poster (identified by name and bot) do not run simultaneously.
func (p *poster) run(ctx context.Context, getOverview overviewFunc, now time.Time) error {
	p.slog.Info("run start", "kind", actionKind, "bot", p.bot, "latest", p.watcher.Latest())
	defer func() {
		p.slog.Info("run end", "kind", actionKind, "bot", p.bot, "latest", p.watcher.Latest())
	}()

	p.runState = make(map[string]*issueState)
	defer func() {
		p.runState = nil
	}()

	// filter reports whether the event with the given key should
	// be processed.
	// A filter on the key avoids the overhead of decoding events
	// we know we don't care about.
	filter := func(key []byte) bool {
		var project, api string
		var issue, id int64
		if err := ordered.Decode(key, &project, &issue, &api, &id); err != nil {
			p.db.Panic("github event decode", "key", storage.Fmt(key), "err", err)
		}
		if !p.projects[project] {
			return false
		}
		if api != "/issues/comments" {
			return false
		}
		if p.alreadyProcessedThisRun(project, issue, id) {
			return false
		}
		return true
	}
	for e := range p.watcher.RecentFiltered(filter) {
		p.maybeProcessIssueComment(ctx, e, getOverview, now)
	}
	return nil
}

// maybeProcessIssueComment determines whether the Issue corresponding to
// the given event (which must be an issue comment in an enabled project) should get a new
// or updated overview.
//
// Issues may be skipped if they have already been processed see [poster.alreadyProcessed],
// or due to their characteristics (see [poster.skip]).
// Non-skipped Issues get an overview (or update) via [poster.logPostOrUpdate].
//
// maybeProcessIssueComment must be run inside a watcher Recent* loop, as it
// marks processed events as old.
func (p *poster) maybeProcessIssueComment(ctx context.Context, e *github.Event,
	getOverview overviewFunc, now time.Time) {
	project, issue, id := e.Project, e.Issue, e.ID
	p.slog.Info("process", "project", project, "issue", issue, "id", id)

	markOld := func(e *github.Event) {
		p.watcher.MarkOld(e.DBTime)
		// Flush immediately to make sure we don't re-process if interrupted later.
		p.watcher.Flush()
		p.slog.Debug("overview: run advanced watcher", "kind", actionKind, "name", p.bot, "latest", p.watcher.Latest(), "event", e.ID)
	}
	if p.alreadyProcessed(project, issue, id) {
		markOld(e)
		return
	}
	lastComment, err := p.logPostOrUpdate(ctx, e, getOverview, now)
	if err != nil {
		p.slog.Error("run", "kind", actionKind, "bot", p.bot, "issue", e.Issue, "event", e, "error", err)
		return
	}
	if lastComment > 0 {
		p.slog.Debug("overview: marking issue as processed", "project", project, "issue", issue, "last comment", lastComment)
		p.markProcessed(e.Project, e.Issue, lastComment)
		markOld(e)
	}
}

// alreadyProcessedThisRun reports whether the issue, as of the given commentID,
// has already been processed by this call to [poster.run].
// a lock on runKey should be held.
func (p *poster) alreadyProcessedThisRun(project string, issue, commentID int64) bool {
	// We don't need to lock runState because [poster.run] handles each event
	// sequentially.
	is := p.runState[string(p.issueStateKey(project, issue))]
	return is != nil && is.LastComment >= commentID
}

// alreadyProcessed reports whether the issue, as of the given commentID,
// has already been processed by this poster, or any poster with the same name
// and bot (by consulting the database).
// a lock on the runKey should be held.
func (p *poster) alreadyProcessed(project string, issue int64, commentID int64) bool {
	return p.lastComment(project, issue) >= commentID
}

// lastComment returns the ID of the last comment processed for this issue.
// a lock on the runKey should be held.
func (p *poster) lastComment(project string, issue int64) int64 {
	return p.getIssueState(project, issue).LastComment
}

// issueState holds state allowing a poster to pick up where it
// left off regarding a particular GitHub issue.
type issueState struct {
	// The ID of the last comment (the comment with the highest ID)
	// at which we have successfully processed this issue.
	//
	// (Meaning we have either decided that this issue
	// does not need an overview given its current state, or
	// we have successfully logged an action in the action log.)
	LastComment int64 `json:"last_comment_id"`
}

// getIssueState returns the stored issue state for the given issue.
// a lock on the runKey should be held.
func (p *poster) getIssueState(project string, issue int64) issueState {
	key := p.issueStateKey(project, issue)
	b, ok := p.db.Get(key)
	if !ok {
		return issueState{LastComment: 0}
	}
	var st issueState
	err := json.Unmarshal(b, &st)
	if err != nil {
		// Unreachable except bug in this package.
		p.db.Panic("poster: could not unmarshal issueState: %s", err)
	}
	return st
}

// issueStateKey returns the key to look up the state of the
// given issue.
func (p *poster) issueStateKey(project string, issue int64) []byte {
	return ordered.Encode(issueStateKind, p.bot, p.name, project, issue)
}

// markProcessed adds an entry to the [poster]'s database
// indicating that the given comment ID and all lower-numbered ones
// have been processed for this issue.
// (If the given comment ID is lower than the latest stored in the
// database, markProcessed is a no-op).
// a lock on runKey should be held.
func (p *poster) markProcessed(project string, issue int64, commentID int64) {
	key := p.issueStateKey(project, issue)
	st := p.getIssueState(project, issue)
	if commentID > st.LastComment {
		st.LastComment = commentID
		p.runState[string(key)] = &st
		p.db.Set(key, storage.JSON(st))
		p.db.Flush()
	}
}

// an overviewFunc returns the overview for the given issue.
type overviewFunc func(context.Context, *github.Issue) (*IssueResult, error)

// logPostOrUpdate logs the appropriate action (post or update) for the event to the action log
// (if an action is needed).
// The event must represent an issue comment in an enabled project.
//
// On success, logPostOrUpdate returns the highest numbered comment present in the Client's
// database for the corresponding issue (which may be higher than the given event's issue comment number).
func (p *poster) logPostOrUpdate(ctx context.Context, e *github.Event, getOverview overviewFunc, now time.Time) (lastComment int64, _ error) {
	p.slog.Info("overview: handling event", "id", e.ID, "project", e.Project,
		"issue", e.Issue, "api", e.API, "dbtime", e.DBTime)

	ghIss, err := github.LookupIssue(p.db, e.Project, e.Issue)
	if err != nil {
		return 0, err
	}

	m, err := p.meta(ghIss)
	if err != nil {
		return 0, err
	}

	p.slog.Debug("overview: handling issue", "project", e.Project, "issue", e.Issue, "metadata", m)

	if skip, reason := p.skip(ghIss, m, now); skip {
		p.slog.Info("overview: skipping issue", "project", e.Project, "issue", e.Issue, "reason", reason)
		// If the issue doesn't need an overview, it should be considered processed.
		return m.LastComment, nil
	}

	p.slog.Debug("overview: getting action for event", "id", e.ID, "id", e.ID, "project", e.Project, "issue", e.Issue, "api", e.API)
	act, err := p.getAction(ctx, ghIss, getOverview)
	if err != nil {
		return 0, err
	}

	p.slog.Info("overview: logging action for event", "action", act, "id", e.ID, "project", e.Project, "issue", e.Issue, "api", e.API)

	if act.isPost() {
		p.logAction(p.db, logPostKey(e.Project, e.Issue), act.encode(), p.requireApproval)
	} else {
		p.logAction(p.db, logUpdateKey(e.Project, e.Issue, m.LastComment), act.encode(), p.requireApproval)
	}

	return m.LastComment, nil
}

// meta returns metadata about an issue that cannot be determined
// based on the issue itself. It consults the poster's database.
func (p *poster) meta(iss *github.Issue) (*issueMeta, error) {
	m := &issueMeta{ignore: func(ic *github.IssueComment) bool {
		return p.skipCommentAuthors[ic.User.Login]
	}}
	for ic := range p.gh.Comments(iss) {
		m.add(ic)
	}
	return m, nil
}

// logUpdateKey returns the key for the "update" action, which may happen
// many times for each issue. The lastComment is the highest numbered comment
// we had seen when this action was registered.
// This is only a portion of the database key; it is prefixed by the poster's action
// kind.
func logUpdateKey(project string, issue int64, lastComment int64) []byte {
	return ordered.Encode(actionContextUpdate, project, issue, lastComment)
}

// logPostKey returns the key for the initial "post" action, which should only happen
// once per issue.
// This is only a portion of the database key; it is prefixed by the poster's action
// kind.
func logPostKey(project string, issue int64) []byte {
	return ordered.Encode(actionContextPost, project, issue)
}

// skip reports whether the given issue should be skipped by this poster,
// and if so, the reason why.
func (p *poster) skip(iss *github.Issue, m *issueMeta, now time.Time) (skip bool, reason string) {
	if iss.PullRequest != nil {
		return true, "pull request"
	}
	if iss.State == "closed" {
		return true, "issue closed"
	}
	tm, err := time.Parse(time.RFC3339, iss.CreatedAt)
	if err != nil {
		return true, fmt.Sprintf("parse CreatedAt failed: %s", err)
	}
	if now.Sub(tm) > p.maxIssueAge {
		return true, fmt.Sprintf("issue too old CreatedAt=%s, maxAge=%s", tm, p.maxIssueAge)
	}
	if p.skipIssueAuthors[iss.User.Login] {
		return true, fmt.Sprintf("issue author %s skipped", iss.User.Login)
	}
	if m.TotalComments-m.SkippedComments < p.minComments {
		return true, fmt.Sprintf("not enough comments ((total(%d) - skipped(%d) < %d)", m.TotalComments, m.SkippedComments, p.minComments)
	}
	return false, ""
}

// comment returns the text of overview comment to post to GitHub,
// including hidden tags to help identify it later.
func comment(s string, w *wrap.Wrapper) (string, error) {
	// These strings may be freely edited.
	body := "\n" + s
	footer := "<sub>(Generated by AI. Emoji vote if this was helpful or unhelpful; more detailed feedback welcome in [this discussion](https://github.com/golang/go/discussions/67901).)</sub>\n"
	c := strings.Join([]string{body, footer}, "\n")
	// Do not remove this wrapping call; it is used to identify the comment.
	return w.Wrap(c, nil)
}

// isOverviewComment reports whether the given comment was authored
// by this poster, without relying on the action log.
func (p *poster) isOverviewComment(ic *github.IssueComment) bool {
	if ic.User.Login == p.bot {
		return false
	}
	if uw := wrap.Parse(ic.Body); uw != nil {
		return uw.Bot == p.bot && uw.Kind == p.name
	}
	return false
}

// findOverviewComment returns the overview comment posted by this poster
// for the given issue, or (nil, nil) if there is none.
// It consults the action log to find the comment. If the comment should
// exist according to the action log, but cannot be found, or the action
// failed or never started, an error is returned.
func (p *poster) findOverviewComment(iss *github.Issue) (*github.IssueComment, error) {
	if ae, ok := actions.Get(p.db, actionKind, logPostKey(iss.Project(), iss.Number)); ok {
		// Post was successful.
		if ae.IsDone() && ae.Error == "" {
			var r result
			if err := json.Unmarshal(ae.Result, &r); err != nil {
				return nil, err
			}
			p.slog.Info("overview: found completed action", "result", r)
			oc, err := p.gh.LookupIssueCommentURL(r.URL)
			if err != nil {
				return nil, err
			}
			return oc, nil
		}

		// The action finished with an error.
		if ae.Error != "" {
			return nil, fmt.Errorf("overview: cannot find existing issue overview (initial post action failed): %s", ae.Error)
		}

		// Post has been registered in the action log but isn't done yet.
		// Perhaps we should try to recover from this, but for now, return an error.
		return nil, fmt.Errorf("overview: cannot find existing issue overview (initial post action not complete)")
	}

	if p.findUnloggedActions {
		// Attempt to find the comment even though it's not present in the action log.
		for ic := range p.gh.Comments(iss) {
			if p.isOverviewComment(ic) {
				p.slog.Warn("overview: found unlogged existing overview comment", "comment", ic)
				return ic, nil
			}
		}
	}

	// No comment action yet.
	return nil, nil
}

// EnableProject enables actions for the given GitHub project.
func (p *poster) EnableProject(project string) {
	p.projects[project] = true
}

// RequireApproval configures the poster to require approval
// for all logged actions.
func (p *poster) RequireApproval() {
	p.requireApproval = true
}

// AutoApprove configures the poster to automatically approve
// all logged actions.
func (p *poster) AutoApprove() {
	p.requireApproval = false
}

// SetMinComments configures the poster to ignore issues with
// fewer than n comments.
func (p *poster) SetMinComments(n int) {
	p.minComments = n
}

// SetMaxIssueAge configures the poster to ignore issues
// that are older than the given age.
func (p *poster) SetMaxIssueAge(age time.Duration) {
	p.maxIssueAge = age
}

// SkipIssueAuthor configures the poster to ignore issues
// authored by the given GitHub user.
func (p *poster) SkipIssueAuthor(author string) {
	if p.skipIssueAuthors == nil {
		p.skipIssueAuthors = map[string]bool{}
	}
	p.skipIssueAuthors[author] = true
}

// SkipCommentsBy configures the poster to ignore comments
// by the given author when determining whether an issue
// has enough comments to get an overview.
func (p *poster) SkipCommentsBy(author string) {
	if p.skipCommentAuthors == nil {
		p.skipCommentAuthors = map[string]bool{}
	}
	p.skipCommentAuthors[author] = true
}

const (
	// The action kind (for the action log).
	actionKind = "overview.PostOrUpdate"

	// Additional context to distinguish a post vs. an update action.
	actionContextPost   = "overview.Post"
	actionContextUpdate = "overview.Update"

	// DB key context for issue state entries.
	issueStateKind = "overview.IssueState"
)

// Default configurations.
var (
	defaultMinComments               = 50
	defaultMaxAge      time.Duration = 365 * 24 * time.Hour // 1 year
)
