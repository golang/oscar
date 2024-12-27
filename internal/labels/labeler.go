// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package labels

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

// A Labeler labels GitHub issues.
// It uses the following database keys:
// - ["labels.Labeler"] for the action log.
// - ["labels.Categories", Project, Issue] to record the categories assigned to an issue.
type Labeler struct {
	slog        *slog.Logger
	db          storage.DB
	github      *github.Client
	cgen        llm.ContentGenerator
	projects    map[string]bool
	watcher     *timed.Watcher[*github.Event]
	name        string
	timeLimit   time.Time
	skipAuthors map[string]bool
	label       bool
	// For the action log.
	requireApproval bool
	actionKind      string
	logAction       actions.BeforeFunc
}

// New creates and returns a new Labeler. It logs to lg, stores state in db,
// manipulates GitHub issues using gh, and classifies issues using cgen.
//
// For the purposes of storing its own state, it uses the given name.
// Future calls to New with the same name will use the same state.
//
// Use the [Labeler] methods to configure the posting parameters
// (especially [Labeler.EnableProject] and [Labeler.EnableLabels])
// before calling [Labeler.Run].
func New(lg *slog.Logger, db storage.DB, gh *github.Client, cgen llm.ContentGenerator, name string) *Labeler {
	l := &Labeler{
		slog:      lg,
		db:        db,
		github:    gh,
		cgen:      cgen,
		projects:  make(map[string]bool),
		watcher:   gh.EventWatcher("labels.Labeler:" + name),
		name:      name,
		timeLimit: time.Now().Add(-defaultTooOld),
	}
	// TODO: Perhaps the action kind should include name, but perhaps not.
	// This makes sure we only ever label each issue once.
	l.actionKind = "labels.Labeler"
	l.logAction = actions.Register(l.actionKind, &actioner{l})
	return l
}

// SetTimeLimit controls how old an issue can be for the Labeler to label it.
// Issues created before time t will be skipped.
// The default is not to post to issues that are more than 48 hours old
// at the time of the call to [New].
func (l *Labeler) SetTimeLimit(t time.Time) {
	l.timeLimit = t
}

const defaultTooOld = 48 * time.Hour

// EnableProject enables the Labeler to post on issues in the given GitHub project (for example "golang/go").
// See also [Labeler.EnableLabels], which must also be called to post anything to GitHub.
func (l *Labeler) EnableProject(project string) {
	l.projects[project] = true
}

// EnableLabels enables the Labeler to label GitHub issues.
// If EnableLabels has not been called, [Labeler.Run] logs what it would post but does not post the messages.
// See also [Labeler.EnableProject], which must also be called to set the projects being considered.
func (l *Labeler) EnableLabels() {
	l.label = true
}

// RequireApproval configures the Labeler to log actions that require approval.
func (l *Labeler) RequireApproval() {
	l.requireApproval = true
}

func (l *Labeler) SkipAuthor(author string) {
	if l.skipAuthors == nil {
		l.skipAuthors = map[string]bool{}
	}
	l.skipAuthors[author] = true
}

// An action has all the information needed to label a GitHub issue.
type action struct {
	Issue      *github.Issue
	Categories []string // the names of the categories corresponding to the labels
	NewLabels  []string // labels to add
}

// result is the result of apply an action.
type result struct {
	URL string // URL of new comment
}

// Run runs a single round of labeling to GitHub.
// It scans all open issues that have been created since the last call to [Labeler.Run]
// using a Labeler with the same name (see [New]).
// Run skips closed issues, and it also skips pull requests.
func (l *Labeler) Run(ctx context.Context) error {
	l.slog.Info("labels.Labeler start", "name", l.name, "label", l.label, "latest", l.watcher.Latest())
	defer func() {
		l.slog.Info("labels.Labeler end", "name", l.name, "latest", l.watcher.Latest())
	}()

	// Ensure that labels in GH match our config.
	for p := range l.projects {
		cats, ok := config.Categories[p]
		if !ok {
			return fmt.Errorf("Labeler.Run: unknown project %q", p)
		}
		if err := l.syncLabels(ctx, p, cats); err != nil {
			return err
		}
	}

	defer l.watcher.Flush()
	for e := range l.watcher.Recent() {
		advance, err := l.logLabelIssue(ctx, e)
		if err != nil {
			l.slog.Error("labels.Labeler", "issue", e.Issue, "event", e, "error", err)
			continue
		}
		eg := slog.Group("event",
			"dbtime", e.DBTime,
			"project", e.Project,
			"issue", e.Issue,
			"json", string(e.JSON))
		if advance {
			l.watcher.MarkOld(e.DBTime)
			// Flush immediately to make sure we don't re-post if interrupted later in the loop.
			l.watcher.Flush()
			l.slog.Info("labels.Labeler advanced watcher", "latest", l.watcher.Latest(), eg)
		} else {
			l.slog.Info("labels.Labeler watcher not advanced", "latest", l.watcher.Latest(), eg)
		}
	}
	return nil
}

// logLabelIssue logs an action to post an issue for the event.
// advance is true if the event should be considered to have been
// handled by this or a previous run function, indicating
// that the Labelers's watcher can be advanced.
// An issue is handled if
//   - labeling is enabled, AND
//   - an issue labeling was successfully logged, or no labeling
//     was needed because no label matched.
//
// Skipped issues are not considered handled.
func (l *Labeler) logLabelIssue(ctx context.Context, e *github.Event) (advance bool, _ error) {
	if skip, reason := l.skip(e); skip {
		l.slog.Info("labels.Labeler skip", "name", l.name, "project",
			e.Project, "issue", e.Issue, "reason", reason, "event", e)
		return false, nil
	}
	// If an action has already been logged for this event, do nothing.
	// we don't need a lock. [actions.before] will lock to avoid multiple log entries.
	if _, ok := actions.Get(l.db, l.actionKind, logKey(e)); ok {
		l.slog.Info("labels.Labeler already logged", "name", l.name, "project", e.Project, "issue", e.Issue, "event", e)
		// If labeling is enabled, we can advance the watcher because
		// a comment has already been logged for this issue.
		return l.label, nil
	}
	// If we didn't skip, it's definitely an issue.
	issue := e.Typed.(*github.Issue)
	l.slog.Debug("labels.Labeler consider", "url", issue.HTMLURL)

	cat, explanation, err := IssueCategory(ctx, l.cgen, e.Project, issue)
	if err != nil {
		return false, fmt.Errorf("IssueCategory(%s): %w", issue.HTMLURL, err)
	}
	l.slog.Info("labels.Labeler chose label", "name", l.name, "project", e.Project, "issue", e.Issue,
		"label", cat.Label, "explanation", explanation)

	if !l.label {
		// Labeling is disabled so we did not handle this issue.
		return false, nil
	}

	act := &action{
		Issue:      issue,
		Categories: []string{cat.Name},
		NewLabels:  []string{cat.Label},
	}
	l.logAction(l.db, logKey(e), storage.JSON(act), l.requireApproval)
	return true, nil
}

func (l *Labeler) skip(e *github.Event) (bool, string) {
	if !l.projects[e.Project] {
		return true, fmt.Sprintf("project %s not enabled for this Labeler", e.Project)
	}
	if want := "/issues"; e.API != want {
		return true, fmt.Sprintf("wrong API %s (expected %s)", e.API, want)
	}
	issue := e.Typed.(*github.Issue)
	if issue.State == "closed" {
		return true, "issue is closed"
	}
	if issue.PullRequest != nil {
		return true, "pull request"
	}
	if author := issue.User.Login; l.skipAuthors[author] {
		return true, fmt.Sprintf("skipping author %q", author)
	}
	return false, ""
}

// syncLabels attempts to reconcile the configured labels in cats with the labels on the issue tracker,
// modifying the issue tracker's labels to match.
// If a label in cats is not on the issue tracker, it is created.
// Otherwise, if the label description on the issue tracker is empty, it is set to the description in the Category.
// Otherwise, if the descriptions don't agree, a warning is logged and nothing is done on the issue tracker.
// This function makes no other changes. In particular, it never deletes labels.
func (l *Labeler) syncLabels(ctx context.Context, project string, cats []Category) error {
	l.slog.Info("syncing labels", "name", l.name, "project", project)
	tlabList, err := l.github.ListLabels(ctx, project)
	if err != nil {
		return err
	}
	tlabs := map[string]github.Label{}
	for _, lab := range tlabList {
		tlabs[lab.Name] = lab
	}

	for _, cat := range cats {
		lab, ok := tlabs[cat.Label]
		if !ok {
			l.slog.Info("creating label", "label", cat.Label)
			if err := l.github.CreateLabel(ctx, project, github.Label{
				Name:        cat.Label,
				Description: cat.Description,
				Color:       labelColor,
			}); err != nil {
				return err
			}
		} else if lab.Description == "" {
			l.slog.Info("setting empty label description", "label", lab.Name)
			if err := l.github.EditLabel(ctx, project, lab.Name, github.LabelChanges{Description: cat.Description}); err != nil {
				return err
			}
		} else if lab.Description != cat.Description {
			l.slog.Warn("descriptions disagree", "label", lab.Name)
		}
	}
	return nil
}

// labelColor is the color of labels created by syncLabels.
const labelColor = "4d0070"

type actioner struct {
	l *Labeler
}

func (ar *actioner) Run(ctx context.Context, data []byte) ([]byte, error) {
	return ar.l.runFromActionLog(ctx, data)
}

func (ar *actioner) ForDisplay(data []byte) string {
	var a action
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}
	return a.Issue.HTMLURL + "\n" + strings.Join(a.NewLabels, ", ")
}

// runFromActionLog is called by actions.Run to execute an action.
// It decodes the action, calls [Labeler.runAction], then encodes the result.
func (l *Labeler) runFromActionLog(ctx context.Context, data []byte) ([]byte, error) {
	var a action
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	res, err := l.runAction(ctx, &a)
	if err != nil {
		return nil, err
	}
	return storage.JSON(res), nil
}

// runAction runs the given action.
func (l *Labeler) runAction(ctx context.Context, a *action) (*result, error) {
	// When updating an issue in GitHub, we must provide all the labels, both the
	// existing and the new.
	//
	// There is an HTTP mechanism for atomic test-and-set, using the If-Match header
	// with an ETag. Unfortunately, GitHub does not support it: it returns a 412
	// Precondition Failed if it sees that header, then makes the change regardless
	// of whether the ETags match. So the best we can do is read the existing labels
	// and immediately write the new ones.
	issue, err := l.github.DownloadIssue(ctx, a.Issue.URL)
	if err != nil {
		return nil, fmt.Errorf("Labeler, download %s: %w", a.Issue.URL, err)
	}

	// Compute the union of the old and new label names.
	oldLabels := map[string]bool{}
	for _, lab := range issue.Labels {
		oldLabels[lab.Name] = true
	}
	newLabels := maps.Clone(oldLabels)
	for _, name := range a.NewLabels {
		newLabels[name] = true
	}
	if maps.Equal(oldLabels, newLabels) {
		// Nothing to do.
		return &result{issue.URL}, nil
	}
	labelNames := slices.Collect(maps.Keys(newLabels))
	// Sort for determinism in tests.
	slices.Sort(labelNames)

	err = l.github.EditIssue(ctx, a.Issue, &github.IssueChanges{Labels: &labelNames})
	// If GitHub returns an error, add it to the action log for this action.
	if err != nil {
		return nil, fmt.Errorf("Labeler: edit %s: %w", a.Issue.URL, err)
	}
	l.setCategories(a.Issue, a.Categories)
	return &result{URL: issue.URL}, nil
}

// logKey returns the key for the event in the action log. This is only a portion
// of the database key; it is prefixed by the Labelers's action kind.
func logKey(e *github.Event) []byte {
	return ordered.Encode(e.Project, e.Issue)
}

// Latest returns the latest known DBTime marked old by the Poster's Watcher.
func (l *Labeler) Latest() timed.DBTime {
	return l.watcher.Latest()
}

func (l *Labeler) setCategories(i *github.Issue, cats []string) {
	l.db.Set(categoriesKey(i.Project(), i.Number), []byte(strings.Join(cats, ",")))
}

// Categories returns the list of categories that the Labeler associated with the given issue.
// If there is no association, it returns nil, false.
func (l *Labeler) Categories(project string, issueNumber int64) ([]string, bool) {
	catstr, ok := l.db.Get(categoriesKey(project, issueNumber))
	if !ok {
		return nil, false
	}
	return strings.Split(string(catstr), ","), true
}

const categoriesPrefix = "labels.Categories"

func categoriesKey(project string, num int64) []byte {
	return ordered.Encode(categoriesPrefix, project, num)
}
