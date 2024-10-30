// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package commentfix implements rule-based rewriting of issue comments.
package commentfix

import (
	"context"
	"encoding/json"
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

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/diff"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/model"
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
	logAction actions.BeforeFunc

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
	f.logAction = actions.Register("commentfix.Fixer:"+name, &actioner{f})
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
	Project string
	Issue   int64
	IC      *issueOrComment
	Body    string // new body of issue or comment
}

// logKey returns the key for the action in the action log.
// The full db key includes the action kind as well, which includes
// the Fixer name.
func (a *action) logKey() []byte {
	return ordered.Encode(a.IC.Post().ID())
}

// result is the result of applying an action.
type result struct {
	URL string // URL of modified issue or comment
}

// Run adds to the action log the configured rewrites to issue texts and comments on GitHub
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
// If [Fixer.EnableEdits] has not been called, Run processes recent issue texts and
// comments and prints diffs of its intended edits to standard error, but it does
// not add the changes to the action log. It also does not mark the issues and comments
// as processed, so that a future call to Run with edits enabled can rewrite them
// on GitHub.
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
		f.logFix(e)
		if f.edit {
			// Mark this one old right now, so that we don't consider editing it again.
			f.watcher.MarkOld(e.DBTime)
			f.watcher.Flush()
			old = 0
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

// LogFixGitHubIssue adds rewrites to the issue body and comments of the
// specified GitHub issue to the action log, following the same logic as [Fixer.Run].
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
func (f *Fixer) LogFixGitHubIssue(ctx context.Context, project string, issue int64) error {
	events := 0
	for event := range f.github.Events(project, issue, issue) {
		events++
		f.logFix(event)
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

// logFix adds an action to fix the specified event to the action log
// if edits are enabled. If edits are disabled or no fix is needed, logFix does nothing.
func (f *Fixer) logFix(e *github.Event) {
	if a := f.newAction(e); a != nil {
		// Don't add the action to the log if edits are off.
		// If we did add it, it could get run; perhaps not now, but in a future time
		// when edits were on.
		if !f.edit {
			return
		}
		key := a.logKey()
		if f.logAction(f.db, key, storage.JSON(a), !actions.RequiresApproval) {
			f.slog.Info("logged action", "key", storage.Fmt(key))
		} else {
			f.slog.Info("fixer already added action", "key", storage.Fmt(key))
		}
	}
}

// newAction returns a new action to take on the issue or comment of the event,
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
		ic = &issueOrComment{Issue: x}
		f.slog.Info("fixer run issue", "dbtime", e.DBTime, "issue", ic.Issue.Number)
	case *github.IssueComment:
		ic = &issueOrComment{Comment: x}
		f.slog.Info("fixer run comment", "dbtime", e.DBTime, "url", ic.Comment.URL)
	}
	if ic.Post().UpdatedAt_().Before(f.timeLimit) {
		return nil
	}
	body, updated := f.Fix(ic.Post().Body_())
	if !updated {
		return nil
	}
	return &action{
		Project: e.Project,
		Issue:   e.Issue,
		IC:      ic,
		Body:    body,
	}
}

type actioner struct {
	f *Fixer
}

func (ar *actioner) Run(ctx context.Context, data []byte) ([]byte, error) {
	return ar.f.runFromActionLog(ctx, data)
}

func (ar *actioner) ForDisplay(data []byte) string {
	var a action
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}
	d := diff.Diff("before", []byte(a.IC.Post().Body_()), "after", []byte(a.Body))
	return a.IC.htmlURL() + "\n" + string(d)
}

// runFromActionLog is called by actions.Run to execute an action.
// It decodes the action, calls [Fixer.runAction], then encodes the result.
func (f *Fixer) runFromActionLog(ctx context.Context, data []byte) ([]byte, error) {
	var a action
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	res, err := f.runAction(ctx, &a)
	if err != nil {
		return nil, err
	}
	return storage.JSON(res), nil
}

// runAction runs the given action.
func (f *Fixer) runAction(ctx context.Context, a *action) (*result, error) {
	// Do not include this Fixer's name in the lock, so that separate
	// fixers cannot operate on the same object at the same time.
	// We need this lock, even though [actions.Run] acquires one.
	// The action log lock includes the fixer name, but this one locks out all fixers.
	lock := string(ordered.Encode("commentfix", a.IC.Post().ID()))
	f.db.Lock(lock)
	defer f.db.Unlock(lock)

	live, err := a.IC.download(ctx, f.github)
	if err != nil {
		// unreachable unless github error
		return nil, fmt.Errorf("commentfix download error: project=%s issue=%d url=%s err=%w", a.Project, a.Issue, a.IC.Post().ID(), err)
	}
	if live.Body_() != a.IC.Post().Body_() {
		f.slog.Info("commentfix stale", "project", a.Project, "issue", a.Issue, "url", a.IC.Post().ID())
		return nil, nil
	}
	f.slog.Info("do commentfix rewrite", "project", a.Project, "issue", a.Issue, "url", a.IC.Post().ID(), "edit", f.edit, "diff", bodyDiff(a.IC.Post().Body_(), a.Body))
	fmt.Fprintf(f.stderr(), "Fix %s:\n%s\n", a.IC.Post().ID(), bodyDiff(a.IC.Post().Body_(), a.Body))

	if !f.edit {
		return nil, nil
	}

	f.slog.Info("commentfix editing github", "url", a.IC.Post().ID())
	if err := a.IC.editBody(ctx, f.github, a.Body); err != nil {
		// unreachable unless github error
		return nil, fmt.Errorf("commentfix edit: project=%s issue=%d err=%w", a.Project, a.Issue, err)
	}
	if !testing.Testing() {
		// unreachable in tests
		time.Sleep(sleep)
	}
	return &result{URL: a.IC.Post().ID()}, nil
}

// Latest returns the latest known DBTime marked old by the Fixer's Watcher.
func (f *Fixer) Latest() timed.DBTime {
	return f.watcher.Latest()
}

type issueOrComment struct {
	Issue   *github.Issue
	Comment *github.IssueComment
}

func (ic *issueOrComment) Post() model.Post {
	if ic.Issue != nil {
		return ic.Issue
	}
	return ic.Comment
}

func (ic *issueOrComment) download(ctx context.Context, gh *github.Client) (model.Post, error) {
	if ic.Issue != nil {
		return gh.DownloadIssue(ctx, ic.Issue.URL)
	}
	return gh.DownloadIssueComment(ctx, ic.Comment.URL)
}

func (ic *issueOrComment) htmlURL() string {
	if ic.Issue != nil {
		return ic.Issue.HTMLURL
	}
	return ic.Comment.HTMLURL
}

func (ic *issueOrComment) editBody(ctx context.Context, gh *github.Client, body string) error {
	if ic.Issue != nil {
		return gh.EditIssue(ctx, ic.Issue, &github.IssueChanges{Body: body})
	}
	return gh.EditIssueComment(ctx, ic.Comment, &github.IssueCommentChanges{Body: body})
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
