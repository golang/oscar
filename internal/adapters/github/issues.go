// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/model"
	"golang.org/x/oscar/internal/storage/timed"
)

// IssueSource returns a [model.Source] providing access to GitHub issues and issue comments.
func (a *Adapter) IssueSource() model.Source[model.Post] {
	return &issueSource{a}
}

type issueSource struct {
	a *Adapter
}

func (s *issueSource) Name() string {
	return "GitHubIssues"
}

// Read implements [model.Source.Read].
func (s *issueSource) Read(ctx context.Context, id string) (model.Post, error) {
	switch {
	case strings.Contains(id, "/issues/comments/"):
		return s.a.ic.DownloadIssueComment(ctx, id)
	case strings.Contains(id, "/issues/"):
		return s.a.ic.DownloadIssue(ctx, id)
	default:
		return nil, fmt.Errorf("github.IssueSource: unknown id %q", id)
	}
}

// Delete implements [model.Source.Delete].
func (*issueSource) Delete(_ context.Context, id string) error {
	return errors.ErrUnsupported
}

// Create implements [model.Source.Create] by
// creating a new issue comment on GitHub.
// Creating new issues is unsupported.
// The Post p must have the new Body and a ParentID referring to the containing issue.
// Other fields of the Post are ignored.
func (s *issueSource) Create(ctx context.Context, p model.Post) (string, error) {
	// PostIssueComment requires an Issue, although only the URL is really needed; Number is for
	// diverted edits.
	iurl := p.ParentID()
	_, num, err := github.ParseIssueURL(iurl)
	if err != nil {
		return "", err
	}
	issue := &github.Issue{
		URL:    iurl,
		Number: num,
	}

	aurl, _, err := s.a.ic.PostIssueComment(ctx, issue, &github.IssueCommentChanges{Body: p.Body_()})
	return aurl, err
}

// Update implements [model.Source.Update] by changing
// an issue or issue comment on GitHub.
// If p is a [*github.Issue], the title, body, state and labels can be changed.
// The keys of changes should be one or more of "title", "body", "state" or "labels".
// (It is not possible to set the title, body or state to the empty string.)
// Labels are replaced, not added to; include all the previous labels in p.Property("labels").
//
// If p is [*github.IssueComment], changes should contain only the key "body", with a new body.
func (s *issueSource) Update(ctx context.Context, p model.Post, changes map[string]any) error {
	switch x := p.(type) {
	default:
		return fmt.Errorf("github.com/Source[Post].Update: bad type %T", p)

	case *github.Issue:
		c := &github.IssueChanges{}
		for k, v := range changes {
			switch k {
			case "title":
				c.Title = v.(string)
			case "body":
				c.Body = v.(string)
			case "state":
				c.State = v.(string)
			case "labels":
				ls := v.([]string)
				c.Labels = &ls
			default:
				return fmt.Errorf("cannot update field %q on GitHub issues", k)
			}
		}
		return s.a.ic.EditIssue(ctx, x, c)

	case *github.IssueComment:
		c := &github.IssueCommentChanges{}
		for k, v := range changes {
			switch k {
			case "body":
				c.Body = v.(string)
			default:
				return fmt.Errorf("cannot update field %q on GitHub issue comments", k)
			}
		}
		return s.a.ic.EditIssueComment(ctx, x, c)
	}
}

// IssueWatcher returns a new [model.Watcher][model.Post] with the given name.
// The Watcher delivers only issues and issue comments, not events or pull requests.
// It picks up where any previous Watcher of the same name left off.
func (a *Adapter) IssueWatcher(name string) model.Watcher[model.Post] {
	return &issueWatcher{a.ic.EventWatcher(name)}
}

type issueWatcher struct {
	w *timed.Watcher[*github.Event]
}

func (w *issueWatcher) Recent() iter.Seq[model.Post] {
	return func(yield func(model.Post) bool) {
		for e := range w.w.Recent() {
			switch x := e.Typed.(type) {
			case *github.Issue:
				if x.PullRequest == nil && !yield(x) {
					return
				}
			case *github.IssueComment:
				if !yield(x) {
					return
				}
			}
		}
	}
}

func (w *issueWatcher) Restart()               { w.w.Restart() }
func (w *issueWatcher) MarkOld(t timed.DBTime) { w.w.MarkOld(t) }
func (w *issueWatcher) Flush()                 { w.w.Flush() }
func (w *issueWatcher) Latest() timed.DBTime   { return w.w.Latest() }
