// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package checks uses the GCP Checks API to check LLM inputs
// and outputs against policies.
package checks

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"golang.org/x/oauth2"
	oauth2google "golang.org/x/oauth2/google"
	"golang.org/x/oscar/internal/llm"
	gcpchecks "google.golang.org/api/checks/v1alpha"
	option "google.golang.org/api/option"
)

// A Checker is an implementation of [llm.PolicyChecker]
// that uses the GCP Checks API.
type Checker struct {
	lg      *slog.Logger
	svc     *gcpchecks.Service // connection to the checks API
	project string

	mu       sync.Mutex      // protects policies
	policies []*PolicyConfig // the policies to apply
}

var _ llm.PolicyChecker = (*Checker)(nil)

// New returns a new Checker.
// gcpproject is the GCP project to use when connecting to the GCP Checks API.
// By default, the Checker has no policies. Use [Checker.SetPolicies] to set a policy.
func New(ctx context.Context, lg *slog.Logger, gcpproject string) (*Checker, error) {
	hc, err := authClient(ctx)
	if err != nil {
		return nil, err
	}
	return newChecker(ctx, lg, gcpproject, hc)
}

func newChecker(ctx context.Context, lg *slog.Logger, gcpproject string, hc *http.Client) (*Checker, error) {
	svc, err := gcpchecks.NewService(
		ctx,
		option.WithEndpoint(api),
		option.WithScopes(scope),
		option.WithHTTPClient(hc),
	)
	if err != nil {
		return nil, err
	}
	return &Checker{
		lg:       lg,
		svc:      svc,
		project:  gcpproject,
		policies: nil,
	}, nil
}

const (
	api   = "https://checks.googleapis.com"
	scope = "https://www.googleapis.com/auth/checks"
)

// Implements [llm.PolicyChecker.SetPolicies].
func (c *Checker) SetPolicies(policies []*llm.PolicyConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.policies = convertPolicies(policies)
}

// Implements [llm.PolicyChecker.CheckText].
func (c *Checker) CheckText(ctx context.Context, text string, prompt ...llm.Part) ([]*llm.PolicyResult, error) {
	req := c.newClassifyRequest(text, prompt)
	resp, err := c.classify(ctx, req)
	if err != nil {
		return nil, err
	}
	return convertResponse(resp), nil
}

// authClient returns an HTTP client authenticated with
// Google Default Application Credentials.
func authClient(ctx context.Context) (*http.Client, error) {
	creds, err := oauth2google.FindDefaultCredentials(ctx)
	if err != nil {
		return nil, err
	}
	return oauth2.NewClient(ctx, creds.TokenSource), nil
}

// Shorthands for [gcpchecks] types.
type (
	TextInput               = gcpchecks.GoogleChecksAisafetyV1alphaTextInput
	RequestContext          = gcpchecks.GoogleChecksAisafetyV1alphaClassifyContentRequestContext
	InputContent            = gcpchecks.GoogleChecksAisafetyV1alphaClassifyContentRequestInputContent
	ClassifyContentRequest  = gcpchecks.GoogleChecksAisafetyV1alphaClassifyContentRequest
	ClassifyContentResponse = gcpchecks.GoogleChecksAisafetyV1alphaClassifyContentResponse
	PolicyConfig            = gcpchecks.GoogleChecksAisafetyV1alphaClassifyContentRequestPolicyConfig
)

// convertPolicies trivially converts a slice of [*llm.PolicyConfig]
// to a slice of [*PolicyConfig].
func convertPolicies(policies []*llm.PolicyConfig) []*PolicyConfig {
	var pc []*PolicyConfig
	for _, p := range policies {
		pc = append(pc, &PolicyConfig{
			PolicyType: string(p.PolicyType),
			Threshold:  p.Threshold,
		})
	}
	return pc
}

// convertPolicies trivially converts the slice of [PolicyResult] in the
// given response to a slice of [*llm.PolicyResult].
func convertResponse(resp *ClassifyContentResponse) []*llm.PolicyResult {
	var pr []*llm.PolicyResult
	for _, p := range resp.PolicyResults {
		pr = append(pr, &llm.PolicyResult{
			PolicyType:      llm.PolicyType(p.PolicyType),
			Score:           p.Score,
			ViolationResult: llm.ViolationResult(p.ViolationResult),
		})
	}
	return pr
}

// newClassifyRequest returns a request to pass to [Checker.classify] containing
// the given text and optional promptParts. If the text represents an input to an LLM,
// promptParts should be empty.
func (c *Checker) newClassifyRequest(text string, promptParts []llm.Part) *ClassifyContentRequest {
	var prompt string
	for _, p := range promptParts {
		switch p := p.(type) {
		case llm.Text:
			prompt += string(p)
		default:
			// Not fatal; the prompt is only used for additional context.
			c.lg.Info("checks.Checker: prompt type not supported", "part", p)
		}
	}
	return &ClassifyContentRequest{
		Context: &RequestContext{
			Prompt: prompt,
		},
		Input: &InputContent{
			TextInput: &TextInput{
				Content:      text,
				LanguageCode: "en",
			},
		},
		Policies: c.policies,
	}
}

const projectHeader = "x-goog-user-project"

// classify makes a classify content request to the GCP Checks Guardrails API
// and returns its result.
func (c *Checker) classify(ctx context.Context, req *ClassifyContentRequest) (*ClassifyContentResponse, error) {
	do := c.svc.Aisafety.ClassifyContent(req)
	do.Header().Add(projectHeader, c.project)
	do.Context(ctx)
	return do.Do()
}

// Scrub is a scrubber for use with [rsc.io/httprr] when writing
// tests that access checks through an httprr.RecordReplay.
// It removes auth credentials and the GCP project from the request.
func Scrub(req *http.Request) error {
	req.Header.Del("Authorization")
	req.Header.Del(projectHeader)
	req.Header.Del("X-Goog-Api-Client") // scrub so http replays work with different versions of Go
	return nil
}
