// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/oscar/internal/bisect"
	"golang.org/x/oscar/internal/github"
)

// parseBisectTrigger extracts a bisection Request, if any,
// from a GitHub issue comment trigger. The expected request
// format for the comment body is as follows:
//
//   - there can be empty lines
//   - the first non-empty line is "@gabyhelp bisect [bad] [good]".
//     Both bad and good are optional, but if one is present so must
//     be the other.
//   - the regression body comes after and it is enclosed with
//     triple backticks (```).
//
// An error is returned if the trigger contains a bisect directive
// but the rest of the comment is not properly formatted.
func parseBisectTrigger(trigger *github.WebhookIssueCommentEvent) (*bisect.Request, error) {
	body := trigger.Comment.Body

	// Find the bisection directive line, if any.
	lines := strings.Split(body, "\n")
	i, bad, good, err := bisectDirectiveLine(lines)
	if err != nil {
		return nil, err
	}
	if i == -1 {
		return nil, nil
	}
	// If bad and good commits are not provided,
	// set them to default values.
	if bad == "" {
		bad = "master"
	}
	if good == "" {
		good = "go1.22.0"
	}

	// Extract the regression code from the rest
	// of the body.
	rest := strings.Join(lines[i+1:], "\n")
	regression := strings.TrimSpace(bisectRegression(rest))
	if regression == "" {
		return nil, errors.New("missing bisect regression")
	}
	return &bisect.Request{
		Trigger: trigger.Comment.URL,
		Issue:   trigger.Issue.URL,
		Repo:    "https://go.googlesource.com/go",
		Fail:    bad,
		Pass:    good,
		Body:    regression,
	}, nil
}

// bisectDirectiveLine checks if there is a line
// encoding a bisection directive. If so, it
// returns the position of the line and it extracts
// the bisect commits from the line. If the line
// is not properly formatted, returns an error.
func bisectDirectiveLine(lines []string) (int, string, string, error) {
	for i, l := range lines {
		l = strings.TrimSpace(l)
		fs := strings.Fields(l)
		if len(fs) < 2 {
			continue
		}
		if fs[0] != "@gabyhelp" || fs[1] != "bisect" {
			continue
		}
		// Bisect directive identified, check for
		// commits and errors.
		if len(fs) == 2 {
			// Only bisect directive is present.
			return i, "", "", nil
		}
		if len(fs) == 4 {
			// Both the bisect directive and
			// two commits are present.
			return i, fs[2], fs[3], nil
		}
		return i, "", "", fmt.Errorf("bisect directive not properly formatted: %s", l)

	}
	return -1, "", "", nil
}

// regressionRegexp matches any text, including
// newlines, that is surrounded by triple backticks.
var regressionRegexp = regexp.MustCompile("(?s)```(.+)```")

// bisectRegression extracts a regression test
// case from body.
func bisectRegression(body string) string {
	matches := regressionRegexp.FindAllStringSubmatch(body, -1)
	if len(matches) != 1 {
		return ""
	}
	rmatches := matches[0]
	if len(rmatches) != 2 {
		return ""
	}
	return rmatches[1]
}
