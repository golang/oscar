// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rules

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"text/template"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
)

// TODO: this is a one-shot request/response version of this feature.
// Implement the version that comments on issues as they come in.

// IssueResult is the result of [Issue].
// It contains the text (in markdown format) of a response to
// that issue mentioning any applicable rules that were violated.
// If Response=="", then nothing to report.
type IssueResult struct {
	Response string
}

// Issue returns text describing the set of rules that the issue does not currently satisfy.
func Issue(ctx context.Context, cgen llm.ContentGenerator, i *github.Issue) (*IssueResult, error) {
	var result IssueResult

	if i.PullRequest != nil {
		result.Response += "## Issue response text\n**None required (pull request)**"
		return &result, nil
	}

	// Extract issue text into a string.
	var issueText bytes.Buffer
	err := template.Must(template.New("prompt").Parse(body)).Execute(&issueText, bodyArgs{
		Title: i.Title,
		Body:  i.Body,
	})
	if err != nil {
		return nil, err
	}

	// Build system prompt to ask about the issue kind.
	var systemPrompt bytes.Buffer
	systemPrompt.WriteString(kindPrompt)
	for _, kind := range rulesConfig.IssueKinds {
		fmt.Fprintf(&systemPrompt, "%s: %s", kind.Name, kind.Text)
		if kind.Details != "" {
			fmt.Fprintf(&systemPrompt, " (%s)", kind.Details)
		}
		systemPrompt.WriteString("\n")
	}

	// Ask about the kind of issue.
	res, err := cgen.GenerateContent(ctx, nil, []llm.Part{llm.Text(systemPrompt.String()), llm.Text(issueText.String())})
	if err != nil {
		return nil, fmt.Errorf("llm request failed: %w\n", err)
	}
	firstLine, remainingLines, _ := strings.Cut(res, "\n")

	// Parse the result.
	var kind IssueKind
	for _, k := range rulesConfig.IssueKinds {
		if firstLine == k.Name {
			kind = k
			break
		}
	}
	if kind.Name == "" {
		log.Printf("kind %q response not valid", firstLine)
		return nil, fmt.Errorf("llm returned invalid kind: %s", firstLine)
		// TODO: just return Response=="" if LLM isn't obeying the prompt?
	}

	// For now, report the classification. We won't do this in the final version.
	result.Response += fmt.Sprintf("## Classification\n**%s**\n\n> %s\n\n", kind.Name, remainingLines)

	// Now that we know the kind, ask about each of the rules for the kind.
	var failed []Rule
	var failedReason []string
	for _, rule := range kind.Rules {
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
		result.Response += "## Issue response text\n**None required**"
		return &result, nil
	}

	var response bytes.Buffer
	fmt.Fprintf(&response, conversationText1)
	for i, rule := range failed {
		fmt.Fprintf(&response, "- %s\n\n", rule.Text)
		fmt.Fprintf(&response, "  > %s\n\n", failedReason[i])
	}
	fmt.Fprintf(&response, conversationText2)
	result.Response += "## Issue response text\n" + response.String()

	return &result, nil
}

//go:embed static/*
var staticFS embed.FS

// TODO: put some of these in the staticFS
const kindPrompt = `
Your job is to categorize Go issues.
The issue is described by a title and a body.
The issue body is encoded in markdown.
Report the category of the issue on a line by itself, followed by an explanation of your decision.
Each category and its description are listed below.

`

const rulePrompt = `
Your job is to decide whether a Go issue follows this rule: %s (%s)
The issue is described by a title and a body.
Report whether the issue is following the rule or not, with a single "yes" or "no"
on a line by itself, followed by an explanation of your decision.
`

const conversationText1 = `
We've identified some possible problems with your issue report. Please review
these findings and fix any that you think are appropriate to fix.

`
const conversationText2 = `
(I'm just a bot; you probably know better than I do whether these findings really need fixing.)

(TODO: Emoji vote if this was helpful or unhelpful.)

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
