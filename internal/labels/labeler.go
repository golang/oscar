// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package labels

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
)

// A Labeler labels GitHub issues.
type Labeler struct {
	slog        *slog.Logger
	db          storage.DB
	github      *github.Client
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
//
//	and watches for new GitHub issues using gh.
//
// For the purposes of storing its own state, it uses the given name.
// Future calls to New with the same name will use the same state.
//
// Use the [Labeler] methods to configure the posting parameters
// (especially [Labeler.EnableProject] and [Labeler.EnableLabels])
// before calling [Labeler.Run].
func New(lg *slog.Logger, db storage.DB, gh *github.Client, name string) *Labeler {
	l := &Labeler{
		slog:      lg,
		db:        db,
		github:    gh,
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
	Issue     *github.Issue
	NewLabels []string // labels to add
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
		if err := l.syncLabels(ctx, p); err != nil {
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
		if advance {
			l.watcher.MarkOld(e.DBTime)
			// Flush immediately to make sure we don't re-post if interrupted later in the loop.
			l.watcher.Flush()
			l.slog.Info("labels.Labeler advanced watcher", "latest", l.watcher.Latest(), "event", e)
		} else {
			l.slog.Info("labels.Labeler watcher not advanced", "latest", l.watcher.Latest(), "event", e)
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
	return false, errors.New("unimplemented")
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

func (l *Labeler) syncLabels(ctx context.Context, project string) error {
	// TODO(jba): generalize to other projects.
	if project != "golang/go" {
		return errors.New("labeling only supported for golang/go")
	}
	l.slog.Info("syncing labels", "name", l.name, "project", project)
	return syncLabels(ctx, l.slog, config.Categories, ghLabels{l.github, project})
}

// trackerLabels manipulates the set of labels on an issue tracker.
// TODO: remove dependence on GitHub.
type trackerLabels interface {
	CreateLabel(ctx context.Context, lab github.Label) error
	EditLabel(ctx context.Context, name string, changes github.LabelChanges) error
	ListLabels(ctx context.Context) ([]github.Label, error)
}

type ghLabels struct {
	gh      *github.Client
	project string
}

func (g ghLabels) CreateLabel(ctx context.Context, lab github.Label) error {
	return g.gh.CreateLabel(ctx, g.project, lab)
}

func (g ghLabels) EditLabel(ctx context.Context, name string, changes github.LabelChanges) error {
	return g.gh.EditLabel(ctx, g.project, name, changes)
}

func (g ghLabels) ListLabels(ctx context.Context) ([]github.Label, error) {
	return g.gh.ListLabels(ctx, g.project)
}

// labelColor is the color of labels created by syncLabels.
const labelColor = "4d0070"

// syncLabels attempts to reconcile the labels in cats with the labels on the issue tracker,
// modifying the issue tracker's labels to match.
// If a label in cats is not on the issue tracker, it is created.
// Otherwise, if the label description on the issue tracker is empty, it is set to the description in the Category.
// Otherwise, if the descriptions don't agree, a warning is logged and nothing is done on the issue tracker.
// This function makes no other changes. In particular, it never deletes labels.
func syncLabels(ctx context.Context, lg *slog.Logger, cats []Category, tl trackerLabels) error {
	tlabList, err := tl.ListLabels(ctx)
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
			lg.Info("creating label", "label", lab.Name)
			if err := tl.CreateLabel(ctx, github.Label{
				Name:        cat.Label,
				Description: cat.Description,
				Color:       labelColor,
			}); err != nil {
				return err
			}
		} else if lab.Description == "" {
			lg.Info("setting empty label description", "label", lab.Name)
			if err := tl.EditLabel(ctx, lab.Name, github.LabelChanges{Description: cat.Description}); err != nil {
				return err
			}
		} else if lab.Description != cat.Description {
			lg.Warn("descriptions disagree", "label", lab.Name)
		}
	}
	return nil
}

type actioner struct {
	l *Labeler
}

func (ar *actioner) Run(ctx context.Context, data []byte) ([]byte, error) {
	// TODO: implement
	return nil, errors.New("unimplemented")
}

func (ar *actioner) ForDisplay(data []byte) string {
	// TODO: implement
	return ""
}
