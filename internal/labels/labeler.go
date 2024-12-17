// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package labels

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
)

// A Labeler labels GitHub issues.
type Labeler struct {
	slog      *slog.Logger
	db        storage.DB
	github    *github.Client
	projects  map[string]bool
	watcher   *timed.Watcher[*github.Event]
	name      string
	timeLimit time.Time
	ignores   []func(*github.Issue) bool
	label     bool
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
