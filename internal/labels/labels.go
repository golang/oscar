// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package labels classifies issues.
package labels

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
)

// A Category is a classification for an issue.
type Category struct {
	Name        string // internal unique name
	Label       string // issue tracker label
	Description string
}

// IssueCategory returns the category chosen by the LLM for the issue, along with an explanation
// of why it was chosen.
func IssueCategory(ctx context.Context, cgen llm.ContentGenerator, iss *github.Issue) (_ Category, explanation string, err error) {
	if iss.PullRequest != nil {
		return Category{}, "", errors.New("issue is a pull request")
	}

	// Extract issue text into a string.
	var issueText bytes.Buffer
	err = template.Must(template.New("body").Parse(body)).Execute(&issueText, bodyArgs{
		Title: iss.Title,
		Body:  iss.Body,
	})
	if err != nil {
		return Category{}, "", err
	}

	// Build system prompt to ask about the issue category.
	var systemPrompt bytes.Buffer
	systemPrompt.WriteString(categoryPrompt)
	for _, cat := range config.Categories {
		fmt.Fprintf(&systemPrompt, "%s: %s\n", cat.Name, cat.Description)
	}

	// Ask about the category of the issue.
	jsonRes, err := cgen.GenerateContent(ctx, responseSchema,
		[]llm.Part{llm.Text(systemPrompt.String()), llm.Text(issueText.String())})
	if err != nil {
		return Category{}, "", fmt.Errorf("llm request failed: %w\n", err)
	}
	var res response
	if err := json.Unmarshal([]byte(jsonRes), &res); err != nil {
		return Category{}, "", fmt.Errorf("unmarshaling %s: %w", jsonRes, err)
	}
	for _, cat := range config.Categories {
		if res.CategoryName == cat.Name {
			return cat, res.Explanation, nil
		}
	}
	return Category{}, "", fmt.Errorf("no category matches LLM response %q", jsonRes)
}

// response is the response that should generated by the LLM.
// It must match [responseSchema].
type response struct {
	CategoryName string
	Explanation  string
}

var responseSchema = &llm.Schema{
	Type: llm.TypeObject,
	Properties: map[string]*llm.Schema{
		"CategoryName": {
			Type:        llm.TypeString,
			Description: "the kind of issue",
		},
		"Explanation": {
			Type:        llm.TypeString,
			Description: "an explanation of why the issue belongs to the category",
		},
	},
}

const categoryPrompt = `
Your job is to categorize Go issues.
The issue is described by a title and a body.
The issue body is encoded in markdown.
Report the category of the issue and an explanation of your decision.
Each category and its description are listed below.

`
const body = `
The title of the issue is: {{.Title}}
The body of the issue is: {{.Body}}
`

type bodyArgs struct {
	Title string
	Body  string
}

var config struct {
	Categories []Category
}

//go:embed static/*
var staticFS embed.FS

func init() {
	f, err := staticFS.Open("static/categories.json")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&config); err != nil {
		log.Fatal(err)
	}
}