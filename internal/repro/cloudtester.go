// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package repro

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"os/exec"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/bisect"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/sandbox"
	"golang.org/x/oscar/internal/storage"
	"rsc.io/ordered"
)

// actionKind is used for the action log.
const actionKind = "repro.Bisect"

// CloudTester implements [CaseTester] and arranges to run bisections
// using a [bisect.Client]. This currently only supports Go test cases.
type CloudTester struct {
	lg           *slog.Logger
	db           storage.DB
	repo         string
	bisectClient *bisect.Client
	logAction    actions.BeforeFunc
	goTester     *GoTester
}

// NewCloudTester returns a new [CloudTester].
// If executor is not nil, it is used to execute commands.
// Otherwise, commands are run in box.
// This will register an action, and as such it should
// only be called once.
func NewCloudTester(ctx context.Context, lg *slog.Logger, db storage.DB, box *sandbox.Sandbox, repo string, bisectClient *bisect.Client, executor Executor) (*CloudTester, error) {
	if repo != goGitRepo {
		return nil, errors.New("NewCloudTester currently only supports the Go repo")
	}

	if executor == nil {
		executor = &cloudExecutor{box}
	}

	gt, err := NewGoTester(ctx, lg, executor)
	if err != nil {
		return nil, err
	}

	ct := &CloudTester{
		lg:           lg,
		db:           db,
		repo:         repo,
		bisectClient: bisectClient,
		goTester:     gt,
	}
	ct.logAction = actions.Register(actionKind, &actioner{ct})
	return ct, nil
}

// Destroy destroys a [CloudTester], removing the temporary directory.
func (ct *CloudTester) Destroy() {
	ct.goTester.Destroy()
}

// Clean adds a package declaration and imports to try to
// make a test case runnable. This implements [CaseTester.Clean].
func (ct *CloudTester) Clean(ctx context.Context, bodyStr string) (string, error) {
	return ct.goTester.Clean(ctx, bodyStr)
}

// CleanVersions tries to make the versions guessed by the LLM valid.
// It implements [CaseTester.CleanVersions].
func (ct *CloudTester) CleanVersions(ctx context.Context, pass, fail string) (string, string) {
	return ct.goTester.CleanVersions(ctx, pass, fail)
}

// Try runs a test case at the suggested version.
// It implements [CaseTester.Try].
func (ct *CloudTester) Try(ctx context.Context, body, version string) (bool, error) {
	return ct.goTester.Try(ctx, body, version)
}

// Bisect creates an action to run a bisection of a test case.
// This implements [CaseTester.Bisect].
func (ct *CloudTester) Bisect(ctx context.Context, issue *github.Issue, body, pass, fail string) (string, error) {
	key := actionLogKey(issue)

	if _, ok := actions.Get(ct.db, actionKind, key); ok {
		ct.lg.Info("repro.Bisect action already recorded", "issue", issue.Number)
		return "", nil
	}

	act := &action{
		Issue: issue,
		Body:  body,
		Pass:  pass,
		Fail:  fail,
	}
	ct.logAction(ct.db, key, storage.JSON(act), true)
	return "", nil
}

// action has all the information needed to run a bisection.
type action struct {
	Issue *github.Issue
	Body  string
	Pass  string
	Fail  string
}

// actioner handles running actions.
// It implements [actions.Actioner].
type actioner struct {
	ct *CloudTester
}

// actionLogKey returns the action key for an issue.
// We only automatically run at most one bisection for one issue.
// People who need more have to use a @gabyhelp request.
func actionLogKey(issue *github.Issue) []byte {
	return ordered.Encode(issue.URL)
}

// Run executes the action.
// It implements [actions.Actioner.Run].
func (ar *actioner) Run(ctx context.Context, data []byte) ([]byte, error) {
	return ar.ct.runFromActionLog(ctx, data)
}

// ForDisplay describes the action for human readers.
// It implements [actions.Actioner.ForDisplay].
func (ar *actioner) ForDisplay(data []byte) string {
	var a action
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}
	return "bisect test case for issue " + a.Issue.HTMLURL + "\n" + html.EscapeString(a.Body)
}

// runFromActionLog is called by actions.Run to execute an action.
func (ct *CloudTester) runFromActionLog(ctx context.Context, data []byte) ([]byte, error) {
	var a action
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	breq := &bisect.Request{
		Trigger: a.Issue.URL,
		Issue:   a.Issue.URL,
		Fail:    a.Fail,
		Pass:    a.Pass,
		Body:    a.Body,
		Repo:    ct.repo,
	}
	return nil, ct.bisectClient.BisectAsync(ctx, breq)
}

// cloudExecutor implements [Executor] by running programs in a sandbox.
type cloudExecutor struct {
	box *sandbox.Sandbox
}

// Execute implements [Executor.Execute].
func (ce *cloudExecutor) Execute(ctx context.Context, lg *slog.Logger, dir, command string, args ...string) ([]byte, error) {
	cmd := ce.box.Command(command, args...)
	cmd.Dir = dir
	return cmd.Output()
}

// LookPath implements [Executor.LookPath].
func (ce *cloudExecutor) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}
