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
	"sync"
	"testing"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
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

// An Example is a GitHub issue along with its correct category.
type Example struct {
	Title    string
	Body     string
	Category string
}

type exampleSpec struct {
	Issue    int64
	Category string
}

// IssueCategory returns the category chosen by the LLM for the issue, along with an explanation
// of why it was chosen. It uses the built-in list of categories for the issue's project.
//
// If there are examples associated with the issue's project, they are added to the prompt.
// The first time this happens for each project, the issues are fetched from the db.
func IssueCategory(ctx context.Context, db storage.DB, cgen llm.ContentGenerator, iss *github.Issue) (_ Category, explanation string, err error) {
	project := iss.Project()
	cats, exs, err := configForProject(db, project)
	if err != nil {
		return Category{}, "", err
	}
	return IssueCategoryFromLists(ctx, cgen, iss, cats, exs)
}

// IssueCategoryFromLists is like [IssueCategory], but uses the given lists of Categories and examples.
func IssueCategoryFromLists(ctx context.Context, cgen llm.ContentGenerator, iss *github.Issue, cats []Category, exs []Example) (_ Category, explanation string, err error) {
	if iss.PullRequest != nil {
		return Category{}, "", errors.New("issue is a pull request")
	}
	bodyDoc := github.ParseMarkdown(iss.Body)
	// First, perform checks that do not rely on an LLM.
	if inv, ok := lookupCategory("invalid", cats); ok && !hasText(bodyDoc) {
		return inv, "body has no text", nil
	}

	prompt, err := buildPrompt(iss.Title, cleanIssueBody(bodyDoc), cats, exs)
	if err != nil {
		return Category{}, "", err
	}
	// Ask the LLM about the category of the issue.
	jsonRes, err := cgen.GenerateContent(ctx, responseSchema, []llm.Part{llm.Text(prompt)})
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

func buildPrompt(title, body string, cats []Category, exs []Example) (string, error) {
	var promptArgs = struct {
		Title      string
		Body       string
		Categories []Category
		Examples   []Example
	}{
		Title:      title,
		Body:       body,
		Categories: cats,
		Examples:   exs,
	}

	var buf bytes.Buffer
	if err := promptTmpl.Execute(&buf, promptArgs); err != nil {
		return "", err
	}
	return buf.String(), nil
}

type promptArgs struct {
	Title      string
	Body       string
	Categories []Category
	Examples   []Example
}

const promptTemplate = `
Your job is to categorize Go issues.
The issue is described by a title and a body.
The issue body is encoded in markdown.
Report the category of the issue and an explanation of your decision.
Each category and its description are listed below.
{{range .Categories}}
{{.Name}}: {{.Description}}
{{.Extra}}
{{end}}
{{if .Examples}}
Here are some examples:
{{range .Examples}}
The following issue should be categorized as {{.Category}}:
The title of the issue is: {{.Title}}
The body of the issue is: {{.Body}}
{{end}}
{{end}}
Here is the issue you should categorize:
The title of the issue is: {{.Title}}
The body of the issue is: {{.Body}}
`

var promptTmpl = template.Must(template.New("prompt").Parse(promptTemplate))

// configForProject returns the categories and examples for the given project.
func configForProject(db storage.DB, project string) ([]Category, []Example, error) {
	cats, ok := config.categories[project]
	if !ok {
		return nil, nil, fmt.Errorf("labels.ConfigForProject: unknown project %q", project)
	}
	config.mu.Lock()
	exs, ok := config.examples[project]
	config.mu.Unlock()
	if !ok {
		var err error
		exs, err = expandExampleSpecs(db, project, config.exampleSpecs[project])
		if err != nil {
			return nil, nil, err
		}
		config.mu.Lock()
		config.examples[project] = exs
		config.mu.Unlock()
	}
	return cats, exs, nil
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

var config struct {
	// Key is project, e.g. "golang/go".
	categories   map[string][]Category
	exampleSpecs map[string][]exampleSpec
	mu           sync.Mutex           // protects examples
	examples     map[string][]Example // from exampleSpecs, with data retrieved from the DB
}

// CategoriesForProject returns the categories used for the given project, or nil if there are none.
func CategoriesForProject(project string) []Category {
	return config.categories[project]
}

func expandExampleSpecs(db storage.DB, project string, specs []exampleSpec) ([]Example, error) {
	// Skip examples during testing.
	if testing.Testing() {
		return nil, nil
	}
	var exs []Example
	for _, spec := range specs {
		iss, err := github.LookupIssue(db, project, spec.Issue)
		if err != nil {
			return nil, err
		}
		ex := Example{
			Title:    iss.Title,
			Body:     cleanIssueBody(github.ParseMarkdown(iss.Body)),
			Category: spec.Category,
		}
		exs = append(exs, ex)
	}
	return exs, nil
}

// Categories returns the list of categories in the db associated with the given issue.
// If there is no association, it returns nil, false.
func Categories(db storage.DB, project string, issueNumber int64) ([]string, bool) {
	catstr, ok := db.Get(categoriesKey(project, issueNumber))
	if !ok {
		return nil, false
	}
	return strings.Split(string(catstr), ","), true
}

//go:embed static/*
var staticFS embed.FS

// Read all category and example files into config.
func init() {
	type catContents struct {
		Project    string
		Categories []Category
	}
	ccontents, err := readAllYAMLFiles[catContents]("static/*-categories.yaml")
	if err != nil {
		log.Fatal(err)
	}
	config.categories = map[string][]Category{}
	for _, cc := range ccontents {
		if cc.Project == "" {
			log.Fatalf("empty or missing project")
		}
		if _, ok := config.categories[cc.Project]; ok {
			log.Fatalf("duplicate project %s", cc.Project)
		}
		config.categories[cc.Project] = cc.Categories
	}

	type exampleContents struct {
		Project  string
		Examples []exampleSpec
	}
	econtents, err := readAllYAMLFiles[exampleContents]("static/*-examples.yaml")
	if err != nil {
		log.Fatal(err)
	}
	config.exampleSpecs = map[string][]exampleSpec{}
	for _, ec := range econtents {
		if ec.Project == "" {
			log.Fatalf("empty or missing project")
		}
		if _, ok := config.exampleSpecs[ec.Project]; ok {
			log.Fatalf("duplicate project %s", ec.Project)
		}
		config.exampleSpecs[ec.Project] = ec.Examples
	}

	config.examples = map[string][]Example{}
	// Populated by expandExampleSpecs.
}

func readAllYAMLFiles[T any](glob string) ([]T, error) {
	files, err := fs.Glob(staticFS, glob)
	if err != nil {
		return nil, err
	}
	var ts []T
	for _, file := range files {
		f, err := staticFS.Open(file)
		if err != nil {
			return nil, err
		}

		var t T
		dec := yaml.NewDecoder(f)
		dec.KnownFields(true)
		if err := dec.Decode(&t); err != nil {
			return nil, fmt.Errorf("%s: %v", file, err)
		}
		ts = append(ts, t)
		f.Close()
	}
	return ts, nil
}
