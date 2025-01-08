// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package repro tries to extract a reproduction case for a bug.
// If it finds one, it tries to bisect to determine what caused the bug.
package repro

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"strings"
	"text/template"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/labels"
	"golang.org/x/oscar/internal/llm"
	"rsc.io/markdown"
)

// CaseTester is an interface that knows how to run
// test cases for the project. This is supplied by
// clients of this package.
type CaseTester interface {
	// Clean takes a test case extracted from an issue
	// and tries to turn it into a runnable test case.
	// For Go this might do things like add a missing package declaration.
	// If the test case is incomprehensible this should return
	// an error, in which case no bisection will be attempted.
	Clean(ctx context.Context, body string) (string, error)

	// CleanVersions takes the pass/fail versions guessed by the LLM,
	// and returns new versions that match the repo for the project
	// being tested.
	CleanVersions(ctx context.Context, passVersion, failVersion string) (string, string)

	// Try runs a cleaned test case at the suggested version.
	// It reports whether the test case passed or failed.
	Try(ctx context.Context, body, version string) (bool, error)

	// Bisect starts a bisection of a cleaned test case.
	// If the Bisect method is able to determine the failing commit,
	// it is responsible for updating the issue.
	// We do this because bisection is done asynchronously.
	//
	// The string result is an arbitrary identifier that
	// will be returned to the caller of [CheckReproduction],
	// and may be used to report progress or cancel the operation.
	Bisect(ctx context.Context, issue *github.Issue, body, pass, fail string) (string, error)
}

// CheckReproduction looks at an issue body and tries to extract
// a test case. If it is able to find a case that has a problem,
// it tries to bisect to the commit that caused the issue.
//
// On success this returns an empty string if there is nothing to do,
// or the result of a call to [CaseTester.Bisect].
func CheckReproduction(ctx context.Context, lg *slog.Logger, cgen llm.ContentGenerator, tester CaseTester, i *github.Issue) (string, error) {
	// See if this is a bug report.
	if i.PullRequest != nil {
		lg.Debug("no reproduction case", "issue", i.Number, "reason", "pull request")
		return "", nil
	}

	// TODO(iant): We shouldn't look up the label again,
	// it should be written down somewhere.
	cat, _, err := labels.IssueCategory(ctx, cgen, i)
	if err != nil {
		return "", err
	}
	if cat.Name != "bug" {
		lg.Debug("no reproduction case", "issue", i.Number, "reason", "not a bug report", "category", cat.Name)
		return "", nil
	}

	bodyDoc := github.ParseMarkdown(i.Body)
	body := cleanIssueBody(bodyDoc)
	args := bodyArgs{
		Title: i.Title,
		Body:  body,
	}

	var sb strings.Builder
	err = template.Must(template.New("repro").Parse(reproPromptTemplate)).Execute(&sb, args)
	if err != nil {
		return "", err
	}

	jsonRes, err := cgen.GenerateContent(ctx, reproSchema, []llm.Part{llm.Text(sb.String())})
	if err != nil {
		return "", err
	}

	var res reproResponse
	if err := json.Unmarshal([]byte(jsonRes), &res); err != nil {
		return "", fmt.Errorf("unmarshaling %q: %w", jsonRes, err)
	}

	if res.Repro == "" || res.Repro == "unknown" {
		lg.Debug("no reproduction case", "issue", i.Number, "reason", "LLM found nothing")
		return "", nil
	}

	// The LLM sometimes introduces Markdown syntax. Remove it.
	repro := strings.ReplaceAll(res.Repro, "```", "")

	testcase, err := tester.Clean(ctx, repro)
	if err != nil {
		lg.Debug("no reproduction case", "issue", i.Number, "reason", "failed to clean test case", "err", err)
		return "", nil
	}

	passRelease, failRelease := tester.CleanVersions(ctx, res.PassRelease, res.FailRelease)

	failOK, err := tester.Try(ctx, testcase, failRelease)
	if err != nil {
		lg.Debug("no reproduction case", "issue", i.Number, "reason", "failed to run test", "version", failRelease, "err", err)
		return "", nil
	}

	passOK, err := tester.Try(ctx, testcase, passRelease)
	if err != nil {
		lg.Debug("no reproduction case", "issue", i.Number, "reason", "failed to run test", "version", passRelease, "err", err)
		return "", nil
	}

	switch {
	case !failOK && passOK:
	case failOK && !passOK:
		failOK, passOK = passOK, failOK
		failRelease, passRelease = passRelease, failRelease
	case failOK && passOK:
		// TODO: try earlier and later revisions.
		lg.Debug("no reproduction case", "issue", i.Number, "reason", "test case always passes", "failVersion", failRelease, "passVersion", passRelease)
		return "", nil
	case !failOK && !passOK:
		// TODO: try earlier and later revisions.
		lg.Debug("no reproduction case", "issue", i.Number, "reason", "test case always fails", "failVersion", failRelease, "passVersion", passRelease)
		return "", nil
	default:
		panic("can't happen")
	}

	// Start the bisection. The bisection code runs asynchronously,
	// and is responsible for updating the issue if it finds the
	// failing commit.
	return tester.Bisect(ctx, i, testcase, passRelease, failRelease)
}

// reproPromptTemplate is the prompt we send to the LLM to ask it to
// pull a reproduction case from an issue.
const reproPromptTemplate = `
Your job is to take an issue reported against the Go language
or standard library and look for a test case,
written in Go, that will reproduce the problem.

If you find a test case, please report the test case,
the Go version where the test case passed,
and the Go version where the case failed.

Only use test cases that actually appear in the issue.
If there is no test case, say that the test case is unknown.
Do not try to write a test case that does not appear in the issue.

Return the test case as a Go program.
Do not use markdown syntax.

Please act as an experienced Go developer and maintainer.

The title of the issue is: {{.Title}}
The body of the issue is: {{.Body}}
`

type bodyArgs struct {
	Title string
	Body  string
}

// reproResponse is the response we expect from the LLM.
// It must match [reproSchema].
type reproResponse struct {
	Repro       string
	FailRelease string
	PassRelease string
}

// reproSchema describes the data the LLM should reply with.
var reproSchema = &llm.Schema{
	Type: llm.TypeObject,
	Properties: map[string]*llm.Schema{
		"Repro": {
			Type:        llm.TypeString,
			Description: `The test case, or "unknown" if no test case is found`,
		},
		"FailRelease": {
			Type:        llm.TypeString,
			Description: `A Go release in which the test fails, or "unknown" if not known`,
		},
		"PassRelease": {
			Type:        llm.TypeString,
			Description: `A Go release in which the test passes, or "unknown" if not known`,
		},
	},
}

// TODO(iant): copied from ../labels/labels.go.
var htmlCommentRegexp = regexp.MustCompile(`<!--(\n|.)*?-->`)

// TODO(iant): copied from ../labels/labels.go.
func cleanIssueBody(doc *markdown.Document) string {
	for b, entry := range blocks(doc) {
		if h, ok := b.(*markdown.HTMLBlock); ok && entry {
			// Delete comments.
			// Each Text is a line.
			t := strings.Join(h.Text, "\n")
			t = htmlCommentRegexp.ReplaceAllString(t, "")
			h.Text = strings.Split(t, "\n")
		}
	}
	return markdown.Format(doc)
}

// TODO(iant): copied from ../labels/labels.go.
var blockType = reflect.TypeFor[markdown.Block]()

// TODO(iant): copied from ../labels/labels.go.
func blocks(b markdown.Block) iter.Seq2[markdown.Block, bool] {
	return func(yield func(markdown.Block, bool) bool) {
		if !yield(b, true) {
			return
		}

		// Using reflection makes this code resilient to additions
		// to the markdown package.

		// All implementations of Block are struct pointers.
		v := reflect.ValueOf(b).Elem()
		if v.Kind() != reflect.Struct {
			fmt.Fprintf(os.Stderr, "internal/labels.blocks: expected struct, got %s", v.Type())
			return
		}
		// Each Block holds its sub-Blocks directly, or in a slice.
		for _, sf := range reflect.VisibleFields(v.Type()) {
			if sf.Type.Implements(blockType) {
				sv := v.FieldByIndex(sf.Index)
				mb := sv.Interface().(markdown.Block)
				for b, e := range blocks(mb) {
					if !yield(b, e) {
						return
					}
				}
			} else if sf.Type.Kind() == reflect.Slice && sf.Type.Elem().Implements(blockType) {
				sv := v.FieldByIndex(sf.Index)
				for i := range sv.Len() {
					mb := sv.Index(i).Interface().(markdown.Block)
					for b, e := range blocks(mb) {
						if !yield(b, e) {
							return
						}
					}
				}
			}
		}
		if !yield(b, false) {
			return
		}
	}
}
