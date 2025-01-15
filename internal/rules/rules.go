// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rules

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"regexp"
	"strings"
	"text/template"
	"time"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/labels"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

// A Poster posts to GitHub about rule violations.
type Poster struct {
	slog      *slog.Logger
	db        storage.DB
	llm       llm.ContentGenerator
	github    *github.Client
	projects  map[string]bool
	watcher   *timed.Watcher[*github.Event]
	name      string
	timeLimit time.Time
	post      bool
	// For the action log.
	requireApproval bool
	actionKind      string
	logAction       actions.BeforeFunc
}

// An action has all the information needed to post a comment to a GitHub issue.
type action struct {
	Issue   *github.Issue
	Changes *github.IssueCommentChanges
}

func New(lg *slog.Logger, db storage.DB, gh *github.Client, llm llm.ContentGenerator, name string) *Poster {
	p := &Poster{
		slog:      lg,
		db:        db,
		llm:       llm,
		github:    gh,
		projects:  make(map[string]bool),
		watcher:   gh.EventWatcher("rules.Poster:" + name),
		name:      name,
		timeLimit: time.Now().Add(-defaultTooOld),
	}
	p.actionKind = "rules.Poster"
	p.logAction = actions.Register(p.actionKind, &actioner{p})
	p.requireApproval = true // TODO: remove. hardcoded for safety, just for now
	return p
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

// RequireApproval configures the Poster to log actions that require approval.
func (p *Poster) RequireApproval() {
	p.requireApproval = true
}

const defaultTooOld = 48 * time.Hour

func (p *Poster) Run(ctx context.Context) error {
	p.slog.Info("rules.Poster start", "name", p.name, "post", p.post, "latest", p.watcher.Latest())
	defer func() {
		p.slog.Info("rules.Poster end", "name", p.name, "latest", p.watcher.Latest())
	}()
	defer p.watcher.Flush()
	for e := range p.watcher.Recent() {
		advance, err := p.logPostIssue(ctx, e)
		if err != nil {
			p.slog.Error("rules.Poster", "issue", e.Issue, "event", e, "error", err)
			continue
		}
		if advance {
			p.watcher.MarkOld(e.DBTime)
			// Flush immediately to make sure we don't re-post if interrupted later in the loop.
			p.watcher.Flush()
		}
	}
	return nil
}

// logKey returns the key for the event in the action log.
// This is only a portion of the database key; it is prefixed by the Poster's action
// kind.
func logKey(e *github.Event) []byte {
	return ordered.Encode(e.Project, e.Issue)
}

func (p *Poster) logPostIssue(ctx context.Context, e *github.Event) (advance bool, _ error) {
	if skip, reason := p.skip(e); skip {
		p.slog.Info("rules.Poster skip", "name", p.name, "project",
			e.Project, "issue", e.Issue, "reason", reason)
		return false, nil
	}
	i, ok := e.Typed.(*github.Issue)
	if !ok {
		p.slog.Error("event does not have *github.Issue type", "typed", e.Typed)
		return true, nil
	}
	// If an action has already been logged for this event, do nothing.
	if _, ok := actions.Get(p.db, p.actionKind, logKey(e)); ok {
		p.slog.Info("rules.Poster already logged", "name", p.name, "project", e.Project, "issue", e.Issue)
		// If posting is enabled, we can advance the watcher because
		// a comment has already been logged for this issue.
		return p.post, nil
	}
	p.slog.Info("rules.Poster considering", "issue", i.Number)
	r, err := Issue(ctx, p.db, p.llm, i, false /*debug*/)
	if err != nil {
		p.slog.Info("rules engine failed", "error", err)
		return false, nil
	}
	if !p.post {
		// Posting is disabled so we did not handle this issue.
		return false, nil
	}
	if r.Response == "" {
		p.slog.Info("no rule violations for", "issue", i.Number)
		return true, nil
	}
	act := &action{
		Issue:   i,
		Changes: &github.IssueCommentChanges{Body: r.Response},
	}
	p.slog.Info("queueing response for", "issue", i.Number, "response", r.Response)
	p.logAction(p.db, logKey(e), storage.JSON(act), p.requireApproval)
	return true, nil
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
		p.slog.Error("rules.Poster parse createdat", "CreatedAt", issue.CreatedAt, "err", err)
		return true, "could not parse createdat"
	}
	if tm.Before(p.timeLimit) {
		return true, fmt.Sprintf("created=%s before time limit=%s", tm, p.timeLimit)
	}
	return false, ""
}

type actioner struct {
	p *Poster
}

func (ar *actioner) Run(ctx context.Context, data []byte) ([]byte, error) {
	p := ar.p
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

func (ar *actioner) ForDisplay(data []byte) string {
	var a action
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}
	return a.Issue.HTMLURL + "\n" + a.Changes.Body
}

type result struct {
	URL string // URL of new comment
}

// runAction runs the given action.
func (p *Poster) runAction(ctx context.Context, a *action) (*result, error) {
	_, url, err := p.github.PostIssueComment(ctx, a.Issue, a.Changes)
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

// Latest returns the latest known DBTime marked old by the Poster's Watcher.
func (p *Poster) Latest() timed.DBTime {
	return p.watcher.Latest()
}

var errPostIssueCommentFailed = errors.New("post issue comment failed")

// IssueResult is the result of [Issue].
// It contains the text (in markdown format) of a response to
// that issue mentioning any applicable rules that were violated.
// If Response=="", then nothing to report.
type IssueResult struct {
	Response string
}

// Issue returns text describing the set of rules that the issue does not currently satisfy.
// If debug==true, then response contains additional llm debugging info.
func Issue(ctx context.Context, db storage.DB, cgen llm.ContentGenerator, i *github.Issue, debug bool) (*IssueResult, error) {
	var result IssueResult

	if i.PullRequest != nil {
		if debug {
			result.Response += "## Issue response text\n**None required (pull request)**"
		}
		return &result, nil
	}

	kind, reasoning, err := Classify(ctx, db, cgen, i)
	if err != nil {
		return nil, err
	}

	if debug {
		result.Response += fmt.Sprintf("## Classification\n**%s**\n\n> %s\n\n", kind.Name, reasoning)
	}

	// Extract issue text into a string.
	var issueText bytes.Buffer
	err = template.Must(template.New("prompt").Parse(body)).Execute(&issueText, bodyArgs{
		Title: i.Title,
		Body:  i.Body,
	})
	if err != nil {
		return nil, err
	}

	// Now that we know the kind, ask about each of the rules for the kind.
	var systemPrompt bytes.Buffer
	var failed []Rule
	var failedReason []string
	for _, rule := range kind.Rules {
		if rule.Regexp != "" {
			// Use a regexp instead of an LLM to detect violations.
			re, err := regexp.Compile(rule.Regexp)
			if err != nil {
				return nil, fmt.Errorf("bad regexp: %w\n", err)
			}
			if re.MatchString(i.Body) {
				failed = append(failed, rule)
				failedReason = append(failedReason, fmt.Sprintf("regexp %s matched", rule.Regexp))
			}
			continue
		}
		// Build system prompt to ask about rule violations.
		systemPrompt.Reset()
		systemPrompt.WriteString(fmt.Sprintf(rulePrompt, rule.Text, rule.Details))

		res, err := cgen.GenerateContent(ctx, nil, []llm.Part{llm.Text(systemPrompt.String()), llm.Text(issueText.String())})
		if err != nil {
			return nil, fmt.Errorf("llm request failed: %w\n", err)
		}
		firstLine, remainingLines, _ := strings.Cut(res, "\n")
		switch firstLine {
		default:
			// LLM failed. Treat as a "yes" so we don't spam
			// people when the LLM is the problem.
			log.Printf("invalid LLM response: %q", firstLine)
			fallthrough
		case "yes":
			// Issue does satisfy the rule, nothing to do.
		case "no":
			failed = append(failed, rule)
			failedReason = append(failedReason, remainingLines)
		}
	}

	if len(failed) == 0 {
		if debug {
			result.Response += "## Issue response text\n**None required**"
		}
		return &result, nil
	}

	var response bytes.Buffer
	fmt.Fprintf(&response, conversationText1)
	for i, rule := range failed {
		fmt.Fprintf(&response, "- %s\n\n", rule.Text)
		if debug {
			fmt.Fprintf(&response, "  > %s\n\n", failedReason[i])
		}
	}
	fmt.Fprintf(&response, conversationText2)
	if debug {
		result.Response += "## Issue response text\n"
	}
	result.Response += response.String()

	return &result, nil
}

// Classify returns the kind of issue we're dealing with.
// Returns a description of the classification and a string describing
// the llm's reasoning.
func Classify(ctx context.Context, db storage.DB, cgen llm.ContentGenerator, i *github.Issue) (IssueKind, string, error) {
	cat, explanation, err := labels.IssueCategory(ctx, db, cgen, i)
	if err != nil {
		return IssueKind{}, "", err
	}
	for _, kind := range rulesConfig.IssueKinds {
		if kind.Name == cat.Name {
			return kind, explanation, nil
		}
	}
	return IssueKind{}, "", fmt.Errorf("unexpected category %s", cat.Name)
}

//go:embed static/*
var staticFS embed.FS

// TODO: put some of these in the staticFS
const rulePrompt = `
Your job is to decide whether a Go issue follows this rule: %s (%s)
The issue is described by a title and a body.
Report whether the issue is following the rule or not, with a single "yes" or "no"
on a line by itself, followed by an explanation of your decision.
`

const conversationText1 = `
We've identified some possible problems with your issue. Please review
these findings and fix any that you think are appropriate to fix.

`
const conversationText2 = `
I'm just a bot; you probably know better than I do whether these findings really need fixing.
<sub>(Emoji vote if this was helpful or unhelpful; more detailed feedback welcome in [this discussion](https://github.com/golang/go/discussions/67901).)</sub>
`

const body = `
The title of the issue is: {{.Title}}
The body of the issue is: {{.Body}}
`

type bodyArgs struct {
	Title string
	Body  string
}

// Structure of JSON configuration file in static/ruleset.json
type RulesConfig struct {
	IssueKinds []IssueKind
}
type IssueKind struct {
	Name    string // name of this kind of issue
	Text    string // one-line description of this kind of issue
	Details string // additional text describing kind of issue to the LLM
	Rules   []Rule // rules that apply to this kind
	Ignore  bool   // don't bother commenting on this kind of issue (just Rules==nil?)
}
type Rule struct {
	Text    string // what we would show to a user
	Details string // additional text for the LLM
	Regexp  string // regular expression that matches for rule violations (instead of an LLM)
}

var rulesConfig RulesConfig

func init() {
	content, err := staticFS.ReadFile("static/ruleset.json")
	if err != nil {
		log.Fatal(err)
	}
	err = json.Unmarshal(content, &rulesConfig)
	if err != nil {
		log.Fatal(err)
	}
}
