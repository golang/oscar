// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/secret"
)

// ValidateWebhookRequest verifies that the request's payload matches
// the HMAC tag in the header and returns a WebhookEvent containing the
// unmarshaled payload. The project argument is the expected GitHub
// project (e.g. "golang/go") for the request.
//
// The function is intended to validate authenticated POST requests
// received from GitHub webhooks.
//
// It expects:
//   - a POST request
//   - an "X-GitHub-Event" header entry with a supported event type
//     ("issues" or "issue_comment")
//   - a non-empty request body containing valid JSON representing an event
//     of the specified event type in the expected GitHub project (e.g. "golang/go")
//   - an "X-Hub-Signature-256" header entry of the form
//     "sha256=HMAC", where HMAC is a valid hex-encoded HMAC tag of the
//     request body computed with the key in db named "github-webhook"
//
// The function returns an error if any of these conditions is not met.
func ValidateWebhookRequest(r *http.Request, project string, db secret.DB) (*WebhookEvent, error) {
	if r.Method != http.MethodPost {
		return nil, fmt.Errorf("%w %s, want %s", errBadHTTPMethod, r.Method, http.MethodPost)
	}

	if r.Body == nil {
		return nil, errNoPayload
	}

	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		return nil, err
	}

	if len(body) == 0 {
		return nil, errNoPayload
	}

	if err := validateWebhookSignature(body, r.Header.Get(xHubSignature256Header), db); err != nil {
		return nil, err
	}

	event, err := toWebhookEvent(r.Header.Get(xGitHubEventHeader), body)
	if err != nil {
		return nil, err
	}

	if event.Project() != project {
		return nil, fmt.Errorf("%w (got %s, want %s)", errWrongProject, event.Project(), project)
	}

	return event, nil
}

// validateWebhookSignature verifies that signature is a string of the
// form "sha256=HMAC", where HMAC s a valid hex-encoded HMAC tag of
// payload, computed with the key in db named "github-webhook".
//
// The function returns an error if any of these conditions is not met.
func validateWebhookSignature(payload []byte, signature string, db secret.DB) error {
	mac, err := parseMAC(signature)
	if err != nil {
		return err
	}

	key, ok := db.Get(githubWebhookSecretName)
	if !ok {
		return errNoKey
	}

	if !validMAC(payload, mac, []byte(key)) {
		return errInvalidHMAC
	}

	return nil
}

// WebhookEvent contains the data sent in a GitHub webhook request that
// is relevant for responding to the event.
type WebhookEvent struct {
	// Payload is the unmarshaled JSON payload of the webhook event,
	// with type corresponding to event as follows:
	//  - "issues": *WebhookIssueEvent
	//  - "issue_comment": *WebhookIssueCommentEvent
	//
	// Many event types are not supported.
	// See https://docs.github.com/en/webhooks/webhook-events-and-payloads
	// for all possible event types.
	Payload any
}

// Project returns the GitHub project (e.g., "golang/go") for the event,
// or an empty string if the project cannot be determined.
func (e *WebhookEvent) Project() string {
	if e == nil || e.Payload == nil {
		return ""
	}
	switch t := e.Payload.(type) {
	case *WebhookIssueEvent:
		return t.Repository.Project
	case *WebhookIssueCommentEvent:
		return t.Repository.Project
	}
	return ""
}

// WebhookIssueEvent is the structure of the JSON payload
// for a GitHub "issues" event (for example, a new issue created).
// https://docs.github.com/en/webhooks/webhook-events-and-payloads#issues
type WebhookIssueEvent struct {
	Action     WebhookIssueAction `json:"action"`
	Repository Repository         `json:"repository"`
	// Additional fields omitted.
}

type WebhookIssueAction string

const (
	WebhookIssueActionOpened WebhookIssueAction = "opened"
	// Additional actions omitted.
)

// WebhookIssueEvent is the structure of the JSON payload
// for a GitHub "issue_comment" event (for example, a new comment posted).
// https://docs.github.com/en/webhooks/webhook-events-and-payloads#issue_comment
type WebhookIssueCommentEvent struct {
	Action     WebhookIssueCommentAction `json:"action"`
	Repository Repository                `json:"repository"`
	// Additional fields omitted.
}

type WebhookIssueCommentAction string

const (
	WebhookIssueCommentActionCreated WebhookIssueCommentAction = "created"
	// Additional actions omitted.
)

// Repository is the repository in which an event occurred.
// https://docs.github.com/en/rest/repos/repos?apiVersion=2022-11-28#get-a-repository
type Repository struct {
	Project string `json:"full_name"`
	// Additional fields omitted.
}

// toWebhookEvent converts data into a WebhookEvent with a Payload of the
// type corresponding to event.
//
// It returns an error if the payload cannot be unmarshaled into the
// event type, or the event type is unrecognized.
func toWebhookEvent(event string, data []byte) (*WebhookEvent, error) {
	w := &WebhookEvent{}

	switch event {
	case "issues":
		w.Payload = new(WebhookIssueEvent)
	case "issue_comment":
		w.Payload = new(WebhookIssueCommentEvent)
	default:
		return nil, fmt.Errorf("%w: %s", errUnknownEvent, event)
	}

	if err := json.Unmarshal(data, w.Payload); err != nil {
		return nil, err
	}
	return w, nil
}

const (
	githubWebhookSecretName = "github-webhook"
	xHubSignature256Header  = "X-Hub-Signature-256"
	xGitHubEventHeader      = "X-GitHub-Event"
)

var (
	errNoKey              = fmt.Errorf("no secret for %q", githubWebhookSecretName)
	errNoPayload          = errors.New("empty payload")
	errInvalidHMAC        = errors.New("invalid HMAC")
	errNoSignatureHeader  = fmt.Errorf("missing %q header entry", xHubSignature256Header)
	errBadSignatureHeader = fmt.Errorf("malformed %q header entry", xHubSignature256Header)
	errUnknownEvent       = errors.New("unrecognized GitHub event type")
	errBadHTTPMethod      = errors.New("unexpected HTTP method")
	errWrongProject       = errors.New("unexpected GitHub project")
)

// parseMAC reads the value of the SHA-256 HMAC tag from the
// signature, which must be of the form "sha256=HMAC", where HMAC is
// a hex-encoded HMAC tag.
// It returns an error if the signature is empty or malformed.
func parseMAC(signature string) ([]byte, error) {
	if signature == "" {
		return nil, errNoSignatureHeader
	}
	hexMAC, ok := strings.CutPrefix(signature, "sha256=")
	if !ok {
		return nil, errBadSignatureHeader
	}
	b, err := hex.DecodeString(hexMAC)
	if err != nil {
		return nil, errBadSignatureHeader
	}
	return b, nil
}

// computeMAC computes the SHA-256 HMAC tag for message with key.
func computeMAC(message []byte, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return mac.Sum(nil)
}

// validMAC reports whether messageMAC is a valid SHA-256 HMAC tag
// for message with key.
func validMAC(message, messageMAC, key []byte) bool {
	expectedMAC := computeMAC(message, key)
	return hmac.Equal(messageMAC, expectedMAC)
}

// ValidWebhookTestdata returns an HTTP request and a secret DB
// (inputs to ValidateWebhookRequest) that will pass validation.
// payload is marshaled into JSON as the body of the returned request.
//
// For testing.
func ValidWebhookTestdata(t *testing.T, event string, payload any) (*http.Request, secret.DB) {
	key := "test-key"
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	signature := computeXHubSignature256(t, body, key)
	return newWebhookRequest(t, event, signature, body), newWebhookSecretDB(t, key)
}

// computeXHubSignature256 returns the expected value of the
// X-Hub-Signature-256 header entry in a GitHub webhook request, of the
// form "sha256=HMAC" where HMAC is the hex-encoded SHA-256 HMAC tag of
// the given payload created with key.
//
// For testing.
func computeXHubSignature256(t *testing.T, payload []byte, key string) string {
	t.Helper()

	h := computeMAC(payload, []byte(key))
	return fmt.Sprintf("sha256=%s", hex.EncodeToString(h))
}

// newWebhookSecretDB returns an in-memory secret DB with a single
// key-value pair {"github-webhook": key}.
//
// For testing.
func newWebhookSecretDB(t *testing.T, key string) secret.DB {
	t.Helper()

	db := secret.Map{}
	db.Set(githubWebhookSecretName, key)
	return db
}

// newWebhookRequest returns an HTTP POST request of the form that would
// be sent by a GitHub webhook, with the request body set to payload,
// and the "X-Hub-Signature-256" header entry set to xHubSignature256.
//
// For testing.
func newWebhookRequest(t *testing.T, event, xHubSignature256 string, payload []byte) *http.Request {
	t.Helper()

	r, err := http.NewRequest(http.MethodPost, "", bytes.NewReader(payload))
	if err != nil {
		t.Fatal("could not create test request")
	}
	if xHubSignature256 != "" {
		r.Header.Set(xHubSignature256Header, xHubSignature256)
	}
	if event != "" {
		r.Header.Set(xGitHubEventHeader, event)
	}
	return r
}
