// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package commentfix implements rule-based rewriting of issue comments.
package commentfix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/oscar/internal/diff"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/markdown"
	"rsc.io/ordered"
)

// A Fixer rewrites issue texts and issue comments using a set of rules.
// After creating a fixer with [New], new rules can be added using
// the [Fixer.AutoLink], [Fixer.ReplaceText], and [Fixer.ReplaceURL] methods,
// and then repeated calls to [Fixer.Run] apply the replacements on GitHub.
//
// The zero value of a Fixer can be used in “offline” mode with [Fixer.Fix],
// which returns rewritten Markdown.
//
// TODO(rsc): Separate the GitHub logic more cleanly from the rewrite logic.
type Fixer struct {
	name      string
	slog      *slog.Logger
	github    *github.Client
	watcher   *timed.Watcher[*github.Event]
	fixes     []func(any, int) any
	projects  map[string]bool
	edit      bool
	timeLimit time.Time
	db        storage.DB

	stderrw io.Writer
}

func (f *Fixer) stderr() io.Writer {
	if f.stderrw != nil {
		return f.stderrw
	}
	return os.Stderr
}

// SetStderr sets the writer to use for messages f intends to print to standard error.
// A Fixer writes directly to standard error (or this writer) so that it can print
// readable multiline debugging outputs. These are also logged via the slog.Logger
// passed to New, but multiline strings format as one very long Go-quoted string in slog
// and are not as easy to read.
func (f *Fixer) SetStderr(w io.Writer) {
	f.stderrw = w
}

// New creates a new Fixer using the given logger and GitHub client.
//
// The Fixer logs status and errors to lg; if lg is nil, the Fixer does not log anything.
//
// The GitHub client is used to watch for new issues and comments
// and to edit issues and comments. If gh is nil, the Fixer can still be
// configured and applied to Markdown using [Fixer.Fix], but calling
// [Fixer.Run] will panic.
//
// The db is the database used to store locks.
//
// The name is the handle by which the Fixer's “last position” is retrieved
// across multiple program invocations; each differently configured
// Fixer needs a different name.
func New(lg *slog.Logger, gh *github.Client, db storage.DB, name string) *Fixer {
	f := &Fixer{
		name:      name,
		slog:      lg,
		github:    gh,
		projects:  make(map[string]bool),
		timeLimit: time.Now().Add(-30 * 24 * time.Hour),
		db:        db,
	}
	f.init() // set f.slog if lg==nil
	if gh != nil {
		f.watcher = gh.EventWatcher("commentfix.Fixer:" + name)
	}
	return f
}

// SetTimeLimit sets the time before which comments are not edited.
func (f *Fixer) SetTimeLimit(limit time.Time) {
	f.timeLimit = limit
}

// init makes sure slog is non-nil.
func (f *Fixer) init() {
	if f.slog == nil {
		f.slog = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
}

func (f *Fixer) EnableProject(name string) {
	f.init()
	if f.github == nil {
		panic("commentfix.Fixer: EnableProject missing GitHub client")
	}
	f.projects[name] = true
}

// EnableEdits configures the fixer to make edits to comments on GitHub.
// If EnableEdits is not called, the Fixer only prints what it would do,
// and it does not mark the issues and comments as “old”.
// This default mode is useful for experimenting with a Fixer
// to gauge its effects.
//
// EnableEdits panics if the Fixer was not constructed by calling [New]
// with a non-nil [github.Client].
func (f *Fixer) EnableEdits() {
	f.init()
	if f.github == nil {
		panic("commentfix.Fixer: EnableEdits missing GitHub client")
	}
	f.edit = true
}

// AutoLink instructs the fixer to turn any text matching the
// regular expression pattern into a link to the URL.
// The URL can contain substitution values like $1
// as supported by [regexp.Regexp.Expand].
//
// For example, to link CL nnn to https://go.dev/cl/nnn,
// you could use:
//
//	f.AutoLink(`\bCL (\d+)\b`, "https://go.dev/cl/$1")
func (f *Fixer) AutoLink(pattern, url string) error {
	f.init()
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	f.fixes = append(f.fixes, func(x any, flags int) any {
		if flags&flagLink != 0 {
			// already inside link
			return nil
		}
		plain, ok := x.(*markdown.Plain)
		if !ok {
			return nil
		}
		var out []markdown.Inline
		start := 0
		text := plain.Text
		for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
			if start < m[0] {
				out = append(out, &markdown.Plain{Text: text[start:m[0]]})
			}
			link := string(re.ExpandString(nil, url, text, m))
			out = append(out, &markdown.Link{
				Inner: []markdown.Inline{&markdown.Plain{Text: text[m[0]:m[1]]}},
				URL:   link,
			})
			start = m[1]
		}
		if start == 0 {
			return nil
		}
		if start < len(text) {
			out = append(out, &markdown.Plain{Text: text[start:]})
		}
		return out
	})
	return nil
}

// ReplaceText instructs the fixer to replace any text
// matching the regular expression pattern with the replacement repl.
// The replacement can contain substitution values like $1
// as supported by [regexp.Regexp.Expand].
//
// ReplaceText only applies in Markdown plain text.
// It does not apply in backticked code text, or in backticked
// or indented code blocks, or to URLs.
// It does apply to the plain text inside headings,
// inside bold, italic, or link markup.
//
// For example, you could correct “cancelled” to “canceled”,
// following Go's usual conventions, with:
//
//	f.ReplaceText(`cancelled`, "canceled")
func (f *Fixer) ReplaceText(pattern, repl string) error {
	f.init()
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	f.fixes = append(f.fixes, func(x any, flags int) any {
		plain, ok := x.(*markdown.Plain)
		if !ok {
			return nil
		}
		if re.FindStringSubmatchIndex(plain.Text) == nil {
			return nil
		}
		plain.Text = re.ReplaceAllString(plain.Text, repl)
		return plain
	})
	return nil
}

// ReplaceURL instructs the fixer to replace any linked URLs
// matching the regular expression pattern with the replacement URL repl.
// The replacement can contain substitution values like $1
// as supported by [regexp.Regexp.Expand].
//
// The regular expression pattern is automatically anchored
// to the start of the URL: there is no need to start it with \A or ^.
//
// For example, to replace links to golang.org with links to go.dev,
// you could use:
//
//	f.ReplaceURL(`https://golang\.org(/?)`, "https://go.dev$1")
func (f *Fixer) ReplaceURL(pattern, repl string) error {
	f.init()
	re, err := regexp.Compile(`\A(?:` + pattern + `)`)
	if err != nil {
		return err
	}
	f.fixes = append(f.fixes, func(x any, flags int) any {
		switch x := x.(type) {
		case *markdown.AutoLink:
			old := x.URL
			x.URL = re.ReplaceAllString(x.URL, repl)
			if x.URL == old {
				return nil
			}
			if x.Text == old {
				x.Text = x.URL
			}
			return x
		case *markdown.Link:
			old := x.URL
			x.URL = re.ReplaceAllString(x.URL, repl)
			if x.URL == old {
				return nil
			}
			if len(x.Inner) == 1 {
				if p, ok := x.Inner[0].(*markdown.Plain); ok && p.Text == old {
					p.Text = x.URL
				}
			}
			return x
		}
		return nil
	})
	return nil
}

// An action has all the information needed to edit a GitHub issue or comment.
type action struct {
	project string
	issue   int64
	ic      *issueOrComment
	body    string // new body of issue or comment
}

// Run applies the configured rewrites to issue texts and comments on GitHub
// that have been updated since the last call to Run for this fixer with edits enabled
// (including in different program invocations using the same fixer name).
//
// By default, Run ignores issues texts and comments more than 30 days old.
// Use [Fixer.SetTimeLimit] to change the cutoff.
//
// Run prints diffs of its edits to standard error in addition to logging them,
// because slog logs the diffs as single-line Go quoted strings that are
// too difficult to skim.
//
// If [Fixer.EnableEdits] has not been called, Run processes recent issue texts
// and comments and prints diffs of its intended edits to standard error,
// but it does not make the changes. It also does not mark the issues and comments as processed,
// so that a future call to Run with edits enabled can rewrite them on GitHub.
//
// Run sleeps for 1 second after each GitHub edit.
//
// Run panics if the Fixer was not constructed by calling [New]
// with a non-nil [github.Client].
func (f *Fixer) Run(ctx context.Context) error {
	if f.watcher == nil {
		return errors.New("commentfix.Fixer: Run missing GitHub client")
	}

	last := timed.DBTime(0)
	old := 0
	const maxOld = 100
	for e := range f.watcher.Recent() {
		if f.edit && last != 0 {
			// Occasionally remember where we were,
			// so if we are repeatedly interrupted we still
			// make progress.
			if old++; old >= maxOld {
				f.watcher.MarkOld(last)
				f.watcher.Flush()
				old = 0
			}
		}
		last = e.DBTime

		applied, err := f.fix(ctx, e)
		if err != nil {
			// unreachable unless github error
			return err
		}
		if !applied {
			continue
		}

		if f.edit {
			// Mark this one old right now, so that we don't consider editing it again.
			f.watcher.MarkOld(e.DBTime)
			f.watcher.Flush()
			old = 0

			if !testing.Testing() {
				// unreachable in tests
				time.Sleep(sleep)
			}
		}
	}

	// Mark the final entry we saw as old.
	// Have to start a new loop because MarkOld must be called during Recent.
	// If another process has moved the mark past last, MarkOld is a no-op.
	if f.edit && last != 0 {
		for range f.watcher.Recent() {
			f.watcher.MarkOld(last)
			f.watcher.Flush()
			break
		}
	}
	return nil
}

// FixGitHubIssue applies rewrites to the issue body and comments of the
// specified GitHub issue, following the same logic as [Fixer.Run].
//
// It requires that the Fixer's [github.Client] contain one or more events
// for the issue.
//
// It does not affect the watcher used by [Fixer.Run] and can be run
// concurrently with [Fixer.Run].
//
// However, any issues or comments for which fixes were applied will not
// be fixed again by subsequent calls to [Fixer.Run] or [Fixer.FixGitHubIssue]
// for a [Fixer] with the same name as this one. This is true even if the
// issue or comment body has changed since the fix was applied, in order
// to a prevent a non-idempotent fix from being applied multiple times.
//
// It returns an error if any of the fixes cannot be applied or if
// no events are found for the issue.
func (f *Fixer) FixGitHubIssue(ctx context.Context, project string, issue int64) error {
	events := 0
	for event := range f.github.Events(project, issue, issue) {
		events++
		applied, err := f.fix(ctx, event)
		if err != nil {
			return err
		}
		if applied && !testing.Testing() {
			time.Sleep(sleep)
		}
	}
	if events == 0 {
		return fmt.Errorf("%w for project=%s issue=%d", errNoGitHubEvents, project, issue)
	}
	return nil
}

var (
	sleep             = 1 * time.Second
	errNoGitHubEvents = errors.New("no GitHub events")
)

// fix creates and runs the action for the specified event.
// applied is true if the action was successfully applied by this run.
// fix returns an error if the action was attempted but could not be applied.
func (f *Fixer) fix(ctx context.Context, e *github.Event) (applied bool, err error) {
	a := f.newAction(e)
	if a == nil {
		return false, nil
	}
	return f.runAction(ctx, a)
}

// appliedKey returns the db key used to indicate a fix has been
// applied by this Fixer (identified by [Fixer.name]) for this issue or
// comment (identified by the URL of the issue/comment).
func (f *Fixer) appliedKey(a *action) []byte {
	return ordered.Encode("commentfix.Fixer", f.name, a.ic.url())
}

// markApplied marks this action as applied by this Fixer
// (identified by [Fixer.name]).
func (f *Fixer) markApplied(a *action) {
	f.db.Set(f.appliedKey(a), nil)
	f.db.Flush()
}

// isApplied reports whether the action has been applied by a Fixer
// with the same name as this one.
func (f *Fixer) isApplied(a *action) bool {
	_, ok := f.db.Get(f.appliedKey(a))
	return ok
}

// newAction returns an newAction to take on the issue or comment of the event,
// or nil if there is nothing to do.
func (f *Fixer) newAction(e *github.Event) *action {
	if !f.projects[e.Project] {
		return nil
	}
	var ic *issueOrComment
	switch x := e.Typed.(type) {
	default: // for example, *github.IssueEvent
		f.slog.Info("fixer skip", "dbtime", e.DBTime, "type", reflect.TypeOf(e.Typed).String())
		return nil
	case *github.Issue:
		if x.PullRequest != nil {
			// Do not edit pull request bodies,
			// because they turn into commit messages
			// and cannot contain things like hyperlinks.
			return nil
		}
		ic = &issueOrComment{issue: x}
		f.slog.Info("fixer run issue", "dbtime", e.DBTime, "issue", ic.issue.Number)
	case *github.IssueComment:
		ic = &issueOrComment{comment: x}
		f.slog.Info("fixer run comment", "dbtime", e.DBTime, "url", ic.comment.URL)
	}
	if tm, err := time.Parse(time.RFC3339, ic.updatedAt()); err == nil && tm.Before(f.timeLimit) {
		return nil
	}
	body, updated := f.Fix(ic.body())
	if !updated {
		return nil
	}
	return &action{
		project: e.Project,
		issue:   e.Issue,
		ic:      ic,
		body:    body,
	}
}

func (f *Fixer) runAction(ctx context.Context, a *action) (applied bool, _ error) {
	// Do not include this Fixer's name in the lock, so that separate
	// fixers cannot operate on the same object at the same time.
	lock := string(ordered.Encode("commentfix", a.ic.url()))
	f.db.Lock(lock)
	defer f.db.Unlock(lock)

	if f.isApplied(a) {
		f.slog.Info("commentfix already applied", "fixer", f.name, "project", a.project, "issue", a.issue, "url", a.ic.url())
		return false, nil
	}

	live, err := a.ic.download(ctx, f.github)
	if err != nil {
		// unreachable unless github error
		return false, fmt.Errorf("commentfix download error: project=%s issue=%d url=%s err=%w", a.project, a.issue, a.ic.url(), err)
	}
	if live.body() != a.ic.body() {
		f.slog.Info("commentfix stale", "project", a.project, "issue", a.issue, "url", a.ic.url())
		return false, nil
	}
	f.slog.Info("commentfix rewrite", "project", a.project, "issue", a.issue, "url", a.ic.url(), "edit", f.edit, "diff", bodyDiff(a.ic.body(), a.body))
	fmt.Fprintf(f.stderr(), "Fix %s:\n%s\n", a.ic.url(), bodyDiff(a.ic.body(), a.body))
	if f.edit {
		f.slog.Info("commentfix editing github", "url", a.ic.url())
		if err := a.ic.editBody(ctx, f.github, a.body); err != nil {
			// unreachable unless github error
			return false, fmt.Errorf("commentfix edit: project=%s issue=%d err=%w", a.project, a.issue, err)
		}
		f.markApplied(a)
		return true, nil
	}
	return false, nil
}

// Latest returns the latest known DBTime marked old by the Fixer's Watcher.
func (f *Fixer) Latest() timed.DBTime {
	return f.watcher.Latest()
}

type issueOrComment struct {
	issue   *github.Issue
	comment *github.IssueComment
}

func (ic *issueOrComment) updatedAt() string {
	if ic.issue != nil {
		return ic.issue.UpdatedAt
	}
	return ic.comment.UpdatedAt
}

func (ic *issueOrComment) body() string {
	if ic.issue != nil {
		return ic.issue.Body
	}
	return ic.comment.Body
}

func (ic *issueOrComment) download(ctx context.Context, gh *github.Client) (*issueOrComment, error) {
	if ic.issue != nil {
		live, err := gh.DownloadIssue(ctx, ic.issue.URL)
		return &issueOrComment{issue: live}, err
	}
	live, err := gh.DownloadIssueComment(ctx, ic.comment.URL)
	return &issueOrComment{comment: live}, err
}

func (ic *issueOrComment) url() string {
	if ic.issue != nil {
		return ic.issue.URL
	}
	return ic.comment.URL
}

func (ic *issueOrComment) editBody(ctx context.Context, gh *github.Client, body string) error {
	if ic.issue != nil {
		return gh.EditIssue(ctx, ic.issue, &github.IssueChanges{Body: body})
	}
	return gh.EditIssueComment(ctx, ic.comment, &github.IssueCommentChanges{Body: body})
}

// Fix applies the configured rewrites to the markdown text.
// If no fixes apply, it returns "", false.
// If any fixes apply, it returns the updated text and true.
func (f *Fixer) Fix(text string) (newText string, fixed bool) {
	p := &markdown.Parser{
		AutoLinkText:  true,
		Strikethrough: true,
		HeadingIDs:    true,
		Emoji:         true,
	}
	doc := p.Parse(text)
	for _, fixer := range f.fixes {
		if f.fixOne(fixer, doc) {
			fixed = true
		}
	}
	if !fixed {
		return "", false
	}
	return markdown.Format(doc), true
}

const (
	// flagLink means this inline is link text,
	// so it is inappropriate/impossible to turn
	// it into a (nested) hyperlink.
	flagLink = 1 << iota
)

// fixOne runs one fix function over doc,
// reporting whether doc was changed.
func (f *Fixer) fixOne(fix func(any, int) any, doc *markdown.Document) (fixed bool) {
	var (
		fixBlock   func(markdown.Block)
		fixInlines func(*[]markdown.Inline)
	)
	fixBlock = func(x markdown.Block) {
		switch x := x.(type) {
		case *markdown.Document:
			for _, sub := range x.Blocks {
				fixBlock(sub)
			}
		case *markdown.Quote:
			for _, sub := range x.Blocks {
				fixBlock(sub)
			}
		case *markdown.List:
			for _, sub := range x.Items {
				fixBlock(sub)
			}
		case *markdown.Item:
			for _, sub := range x.Blocks {
				fixBlock(sub)
			}
		case *markdown.Heading:
			fixBlock(x.Text)
		case *markdown.Paragraph:
			fixBlock(x.Text)
		case *markdown.Text:
			fixInlines(&x.Inline)
		}
	}

	link := 0
	fixInlines = func(inlines *[]markdown.Inline) {
		changed := false
		var out []markdown.Inline
		for _, x := range *inlines {
			switch x := x.(type) {
			case *markdown.Del:
				fixInlines(&x.Inner)
			case *markdown.Emph:
				fixInlines(&x.Inner)
			case *markdown.Strong:
				fixInlines(&x.Inner)
			case *markdown.Link:
				link++
				fixInlines(&x.Inner)
				link--
			}
			flags := 0
			if link > 0 {
				flags = flagLink
			}
			switch fx := fix(x, flags).(type) {
			default:
				// unreachable unless bug in fix func
				f.slog.Error("fixer returned invalid type", "old", reflect.TypeOf(x).String(), "new", reflect.TypeOf(fx).String())
				out = append(out, x)
			case nil:
				out = append(out, x)
			case markdown.Inline:
				changed = true
				out = append(out, fx)
			case []markdown.Inline:
				changed = true
				out = append(out, fx...)
			}
		}
		if changed {
			*inlines = out
			fixed = true
		}
	}

	fixBlock(doc)
	return fixed
}

func bodyDiff(old, new string) string {
	old = strings.TrimRight(old, "\n") + "\n"
	old = strings.ReplaceAll(old, "\r\n", "\n")

	new = strings.TrimRight(new, "\n") + "\n"
	new = strings.ReplaceAll(new, "\r\n", "\n")

	return string(diff.Diff("old", []byte(old), "new", []byte(new)))
}
