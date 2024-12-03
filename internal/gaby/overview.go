// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/safehtml"
	"github.com/google/safehtml/template"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/htmlutil"
	"golang.org/x/oscar/internal/llmapp"
	"golang.org/x/oscar/internal/overview"
	"golang.org/x/oscar/internal/search"
)

// overviewPage holds the fields needed to display the results
// of a search.
type overviewPage struct {
	CommonPage

	Params overviewParams // the raw query params
	Result *overviewResult
	Error  error // if non-nil, the error to display instead of the result
}

type overviewResult struct {
	Raw   *llmapp.Result // the raw result
	Issue *github.Issue  // the issue that was analyzed
	Typed any            // additional, type-specific results
	Type  string         // the type of result
	// a text description of the type of result (for display), finishing
	// the sentence "AI-generated Overview of ".
	Desc string
}

// overviewParams holds the raw HTML parameters.
type overviewParams struct {
	Query           string // the issue ID to lookup, or golang/go#12345 or github.com/golang/go/issues/12345 form
	LastReadComment string // (for [updateOverviewType]: summarize all comments after this comment ID)
	OverviewType    string // the type of overview to generate
}

// the possible overview types
const (
	issueOverviewType   = "issue_overview"
	relatedOverviewType = "related_overview"
	updateOverviewType  = "update_overview"
)

// validOverviewType reports whether the given type
// is a recognized overview type.
func validOverviewType(t string) bool {
	return t == issueOverviewType || t == relatedOverviewType || t == updateOverviewType
}

func (g *Gaby) handleOverview(w http.ResponseWriter, r *http.Request) {
	handlePage(w, g.populateOverviewPage(r), overviewPageTmpl)
}

// fixMarkdown fixes mistakes that we have observed the LLM make
// when generating markdown.
func fixMarkdown(text string) string {
	// add newline after backticks followed by a space
	return strings.ReplaceAll(text, "``` ", "```\n")
}

var overviewPageTmpl = newTemplate(overviewPageTmplFile, template.FuncMap{
	"fmttime": fmtTimeString,
})

// fmtTimeString formats an [time.RFC3339]-encoded time string
// as a [time.DateOnly] time string.
func fmtTimeString(s string) string {
	if s == "" {
		return s
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Format(time.DateOnly)
}

// parseIssueNumber parses the issue number from the given issue ID string.
// The issue ID string can be in one of the following formats:
//   - "12345" (by default, assume it is a golang/go project's issue)
//   - "golang/go#12345"
//   - "github.com/golang/go/issues/12345" or "https://github.com/golang/go/issues/12345"
//   - "go.dev/issues/12345" or "https://go.dev/issues/12345"
func parseIssueNumber(issueID string) (project string, issue int64, _ error) {
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return "", 0, nil
	}
	split := func(q string) (string, string) {
		q = strings.TrimPrefix(q, "https://")
		// recognize github.com/golang/go/issues/12345
		if proj, ok := strings.CutPrefix(q, "github.com/"); ok {
			i := strings.LastIndex(proj, "/issues/")
			if i < 0 {
				return "", q
			}
			return proj[:i], proj[i+len("/issues/"):]
		}
		// recognize "go.dev/issues/12345"
		if num, ok := strings.CutPrefix(q, "go.dev/issues/"); ok {
			return "golang/go", num
		}
		// recognize golang/go#12345
		if proj, num, ok := strings.Cut(q, "#"); ok {
			return proj, num
		}
		return "", q
	}
	proj, num := split(issueID)
	issue, err := strconv.ParseInt(num, 10, 64)
	if err != nil || issue <= 0 {
		return "", 0, fmt.Errorf("invalid issue number %q", issueID)
	}
	return proj, issue, nil
}

// parseIssueComment parses the issue comment ID from the given commentID string.
// The issue ID string can be in one of the following formats:
//   - "6789": returns 6789 (assumed to be issue comment 6789 for the issue
//     in [overviewParams.Query] in the "golang/go" repo)
//
// TODO(tatianabradley): allow comments to be expressed as URLs, e.g.:
//   - "golang/go#12345#issuecomment6789"
//   - "github.com/golang/go/issues/12345#issuecomment6789" or "https://github.com/golang/go/issues/12345#issuecomment6789"
//   - "go.dev/issues/12345#issuecomment6789" or "https://go.dev/issues/12345#issuecomment6789"
func parseIssueComment(commentID string) (int64, error) {
	commentID = trim(commentID)
	return strconv.ParseInt(commentID, 10, 64)
}

// populateOverviewPage returns the contents of the overview page.
func (g *Gaby) populateOverviewPage(r *http.Request) *overviewPage {
	pm := overviewParams{
		Query:           r.FormValue(paramQuery),
		OverviewType:    r.FormValue(paramOverviewType),
		LastReadComment: r.FormValue(paramLastRead),
	}
	p := &overviewPage{
		Params: pm,
	}
	p.setCommonPage()
	if trim(p.Params.Query) == "" {
		return p
	}
	overview, err := g.newOverview(r.Context(), &p.Params)
	if err != nil {
		p.Error = err
		return p
	}
	p.Result = overview
	return p
}

func (p *overviewPage) setCommonPage() {
	p.CommonPage = CommonPage{
		ID:          overviewID,
		Description: "Generate overviews of golang/go issues and their comments, or summarize the relationship between a golang/go issue and its related documents.",
		FeedbackURL: "https://github.com/golang/oscar/issues/61#issuecomment-new",
		Styles:      []safeURL{searchID.CSS()},
		Form: Form{
			Inputs:     p.Params.inputs(),
			SubmitText: "generate",
		},
	}
}

const (
	paramOverviewType = "t"
	paramLastRead     = "last_read"
)

var (
	safeLastRead = toSafeID(paramLastRead)
)

// inputs converts the params to HTML form inputs.
func (pm *overviewParams) inputs() []FormInput {
	return []FormInput{
		{
			Label:       "issue",
			Type:        "int or string",
			Description: "the issue to summarize, as a number or URL (e.g. 1234, golang/go#1234, or https://github.com/golang/go/issues/1234)",
			Name:        safeQuery,
			Required:    true,
			Typed: TextInput{
				ID:    safeQuery,
				Value: pm.Query,
			},
		},
		{
			Label:       "overview type",
			Type:        "radio choice",
			Description: `"issue and comments" generates an overview of the issue and its comments; "related documents" searches for related documents and summarizes them; "comments after" generates a summary of the comments after the specified comment ID`,
			Name:        toSafeID(paramOverviewType),
			Required:    true,
			Typed: RadioInput{
				Choices: []RadioChoice{
					{
						Label:   "issue overview",
						ID:      toSafeID(issueOverviewType),
						Value:   issueOverviewType,
						Checked: pm.checkRadio(issueOverviewType),
					},
					{
						Label:   "related documents",
						ID:      toSafeID(relatedOverviewType),
						Value:   relatedOverviewType,
						Checked: pm.checkRadio(relatedOverviewType),
					},
					{
						Label: "comments after",
						Input: &FormInput{
							Name: safeLastRead,
							Typed: TextInput{
								ID:    safeLastRead,
								Value: pm.LastReadComment,
							},
						},
						ID:      toSafeID(updateOverviewType),
						Value:   updateOverviewType,
						Checked: pm.checkRadio(updateOverviewType),
					},
				},
			},
		},
	}
}

// checkRadio reports whether radio button with the given value
// should be checked.
func (f *overviewParams) checkRadio(value string) bool {
	// If the overview type is not set, or is set to an invalid value,
	// the default option (issue overview) should be checked.
	if !validOverviewType(f.OverviewType) {
		return value == issueOverviewType
	}

	// Otherwise, the button corresponding to the result
	// type should be checked.
	return value == f.OverviewType
}

// newOverview generates an newOverview of the issue based on the given parameters.
func (g *Gaby) newOverview(ctx context.Context, pm *overviewParams) (*overviewResult, error) {
	proj, issue, err := parseIssueNumber(pm.Query)
	if err != nil {
		return nil, fmt.Errorf("invalid form value: %v", err)
	}
	if proj == "" && len(g.githubProjects) > 0 {
		proj = g.githubProjects[0] // default to first project.
	}
	if !slices.Contains(g.githubProjects, proj) {
		return nil, fmt.Errorf("invalid form value (unrecognized project): %q", pm.Query)
	}
	iss, err := github.LookupIssue(g.db, proj, issue)
	if err != nil {
		return nil, err
	}

	switch pm.OverviewType {
	case "", issueOverviewType:
		return g.issueOverview(ctx, iss)
	case relatedOverviewType:
		return g.relatedOverview(ctx, iss)
	case updateOverviewType:
		lastReadComment, err := parseIssueComment(pm.LastReadComment)
		if err != nil {
			return nil, err
		}
		return g.updateOverview(ctx, iss, lastReadComment)
	default:
		return nil, fmt.Errorf("unknown overview type %q", pm.OverviewType)
	}
}

// issueOverview generates an overview of the issue and its comments.
func (g *Gaby) issueOverview(ctx context.Context, iss *github.Issue) (*overviewResult, error) {
	overview, err := g.overview.ForIssue(ctx, iss)
	if err != nil {
		return nil, err
	}
	return &overviewResult{
		Raw:   overview.Overview,
		Issue: iss,
		Typed: overview,
		Type:  issueOverviewType,
		Desc:  fmt.Sprintf("issue %d and all %d comments", iss.Number, overview.TotalComments),
	}, nil
}

// relatedOverview generates an overview of the issue and its related documents.
func (g *Gaby) relatedOverview(ctx context.Context, iss *github.Issue) (*overviewResult, error) {
	analysis, err := search.Analyze(ctx, g.llmapp, g.vector, g.docs, iss.DocID())
	if err != nil {
		return nil, err
	}
	return &overviewResult{
		Raw:   &analysis.Result,
		Issue: iss,
		Typed: analysis,
		Type:  relatedOverviewType,
		Desc:  fmt.Sprintf("issue %d and %d related docs", iss.Number, len(analysis.Output.Related)),
	}, nil
}

// updateOverview generates an overview of the issue and its comments, split
// into "old" and "new" groups by lastReadComment.
func (g *Gaby) updateOverview(ctx context.Context, iss *github.Issue, lastReadComment int64) (*overviewResult, error) {
	overview, err := g.overview.ForIssueUpdate(ctx, iss, lastReadComment)
	if err != nil {
		return nil, err
	}
	return &overviewResult{
		Raw:   overview.Overview,
		Issue: iss,
		Typed: overview,
		Type:  updateOverviewType,
		Desc:  fmt.Sprintf("issue %d and its %d new comments after %d", iss.Number, overview.NewComments, lastReadComment),
	}, nil
}

// Related returns the relative URL of the related-entity search
// for the issue. This is used in the overview page template.
func (r *overviewResult) Related() string {
	return fmt.Sprintf("/search?q=%s", r.Issue.HTMLURL)
}

// TotalComments returns the total number of comments for the
// analyzed issue, or 0 if not known.
func (r *overviewResult) TotalComments() int {
	if ir, ok := r.Typed.(*overview.IssueResult); ok {
		return ir.TotalComments
	}
	return 0
}

// Display returns the overview result as safe HTML.
func (r *overviewResult) Display() safehtml.HTML {
	switch r.Type {
	case issueOverviewType, updateOverviewType:
		md := r.Raw.Response
		md = fixMarkdown(md)
		return htmlutil.MarkdownToSafeHTML(md)
	case relatedOverviewType:
		return displayRelated(r.Typed.(*search.Analysis))
	}
	return safehtml.HTML{}
}

// Template for converting a [search.Analysis] to Markdown.
const relatedMD = `
## Original Post

{{.Output.Summary}}

## Related Documents
{{range .Output.Related}}
### {{.Title}} ([{{.URL}}]({{.URL}}))

* **Summary**: {{.Summary}}
* **Relationship**: {{.Relationship}}
* **Relevance**: {{.Relevance}}
* **Relevance reason**: {{.RelevanceReason}}
{{end}}

`

var relatedMDTmpl = template.Must(template.New("relatedMD").Parse(relatedMD))

// displayRelated returns the result of a related documents
// analysis as safe HTML.
func displayRelated(a *search.Analysis) safehtml.HTML {
	// Convert to Markdown, then HTML.
	// We could instead convert directly to HTML, but Markdown is easier
	// to work with, and we will likely publish these summaries to GitHub,
	// which uses Markdown.
	var buf bytes.Buffer
	if err := relatedMDTmpl.Execute(&buf, a); err != nil {
		panic(err)
	}
	return htmlutil.MarkdownToSafeHTML(buf.String())
}
