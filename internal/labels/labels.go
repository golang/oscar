// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package labels classifies issues.
//
// The categories it uses are stored in static/*-categories.yaml
// files, one file per project.
package labels

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"iter"
	"log"
	"os"
	"reflect"
	"regexp"
	"strings"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
	"gopkg.in/yaml.v3"
	"rsc.io/markdown"
)

// A Category is a classification for an issue.
type Category struct {
	Name        string // internal unique name
	Label       string // issue tracker label
	Description string // should match issue tracker
	Extra       string // additional description, not in issue tracker
}

// IssueCategory returns the category chosen by the LLM for the issue, along with an explanation
// of why it was chosen. It uses the built-in list of categories.
func IssueCategory(ctx context.Context, cgen llm.ContentGenerator, project string, iss *github.Issue) (_ Category, explanation string, err error) {
	cats, ok := config.Categories[project]
	if !ok {
		return Category{}, "", fmt.Errorf("IssueCategory: unknown project %q", project)
	}
	return IssueCategoryFromList(ctx, cgen, iss, cats)
}

// IssueCategoryFromList is like [IssueCategory], but uses the given list of Categories.
func IssueCategoryFromList(ctx context.Context, cgen llm.ContentGenerator, iss *github.Issue, cats []Category) (_ Category, explanation string, err error) {
	if iss.PullRequest != nil {
		return Category{}, "", errors.New("issue is a pull request")
	}
	bodyDoc := github.ParseMarkdown(iss.Body)
	// First, perform checks that do not rely on an LLM.
	if inv, ok := lookupCategory("invalid", cats); ok && !hasText(bodyDoc) {
		return inv, "body has no text", nil
	}

	body := cleanIssueBody(bodyDoc)
	// Extract issue text into a string.
	var issueText bytes.Buffer
	err = template.Must(template.New("body").Parse(bodyTemplate)).Execute(&issueText, bodyArgs{
		Title: iss.Title,
		Body:  body,
	})
	if err != nil {
		return Category{}, "", err
	}

	// Build system prompt to ask about the issue category.
	var systemPrompt bytes.Buffer
	systemPrompt.WriteString(categoryPrompt)
	for _, cat := range cats {
		fmt.Fprintf(&systemPrompt, "%s: %s\n%s\n\n", cat.Name, cat.Description, cat.Extra)
	}

	// Ask the LLM about the category of the issue.
	jsonRes, err := cgen.GenerateContent(ctx, responseSchema,
		[]llm.Part{llm.Text(systemPrompt.String()), llm.Text(issueText.String())})
	if err != nil {
		return Category{}, "", fmt.Errorf("llm request failed: %w\n", err)
	}
	var res response
	if err := json.Unmarshal([]byte(jsonRes), &res); err != nil {
		return Category{}, "", fmt.Errorf("unmarshaling %s: %w", jsonRes, err)
	}
	cat, ok := lookupCategory(res.CategoryName, cats)
	if ok {
		return cat, res.Explanation, nil
	}
	return Category{}, "", fmt.Errorf("no category matches LLM response %q", jsonRes)
}

// hasText reports whether doc has any text blocks.
func hasText(doc *markdown.Document) bool {
	inHeading := 0
	for b, entry := range blocks(doc) {
		switch b.(type) {
		case *markdown.Text:
			// Ignore text in headings.
			if inHeading == 0 {
				return true
			}
		case *markdown.Heading:
			if entry {
				inHeading++
			} else {
				inHeading--
			}
		}
	}
	return false
}

// lookupCategory returns the Category in cats with the given
// name, and true. If there is none, the second return value is false.
func lookupCategory(name string, cats []Category) (Category, bool) {
	for _, cat := range cats {
		if cat.Name == name {
			return cat, true
		}
	}
	return Category{}, false
}

// TODO(jba): this is approximate.
// See https://developer.mozilla.org/en-US/docs/Web/HTML/Comments for the exact syntax.
var htmlCommentRegexp = regexp.MustCompile(`<!--(\n|.)*?-->`)

// cleanIssueBody adjusts the issue body to improve the odds that it will be properly
// labeled.
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

var blockType = reflect.TypeFor[markdown.Block]()

// blocks returns an iterator over the blocks of b, including
// b itself. The traversal is top-down, preorder.
// Each block is yielded twice: first on entry, with the second
// value true; then on exit, with the second value false.
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
const bodyTemplate = `
The title of the issue is: {{.Title}}
The body of the issue is: {{.Body}}
`

type bodyArgs struct {
	Title string
	Body  string
}

var config struct {
	// Key is project, e.g. "golang/go".
	Categories map[string][]Category
}

//go:embed static/*
var staticFS embed.FS

// Read all category files into config.
func init() {
	catFiles, err := fs.Glob(staticFS, "static/*-categories.yaml")
	if err != nil {
		log.Fatal(err)
	}
	config.Categories = map[string][]Category{}
	for _, file := range catFiles {
		f, err := staticFS.Open(file)
		if err != nil {
			log.Fatalf("%s: %v", file, err)
		}

		var contents struct {
			Project    string
			Categories []Category
		}

		dec := yaml.NewDecoder(f)
		dec.KnownFields(true)
		if err := dec.Decode(&contents); err != nil {
			log.Fatalf("%s: %v", file, err)
		}
		if contents.Project == "" {
			log.Fatalf("%s: empty or missing project", file)
		}
		if _, ok := config.Categories[contents.Project]; ok {
			log.Fatalf("%s: duplicate project %s", file, contents.Project)
		}
		config.Categories[contents.Project] = contents.Categories
		f.Close()
	}
}
