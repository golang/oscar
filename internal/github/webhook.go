// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/secret"
)

// ValidateWebhookRequest verifies that the request's payload matches
// the HMAC tag in the header and returns the raw payload.
//
// It is intended to validate authenticated POST requests received
// from GitHub webhooks.
//
// It expects:
//   - a POST request with a non-empty body
//   - a "X-Hub-Signature-256" header entry of the form
//     "sha256=HMAC", where HMAC is a valid hex-encoded HMAC tag of the
//     request body computed with the key in db named "github-webhook"
//
// The function returns an error if any of these conditions is not met.
func ValidateWebhookRequest(r *http.Request, db secret.DB) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, fmt.Errorf("unexpected HTTP method %s, want %s", r.Method, http.MethodPost)
	}

	if r.Body == nil {
		return nil, errNoPayload
	}

	data, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return nil, errNoPayload
	}

	key, ok := db.Get(githubWebhookSecretName)
	if !ok {
		return nil, errNoKey
	}

	mac, err := parseMAC(&r.Header)
	if err != nil {
		return nil, err
	}

	if !validMAC(data, mac, []byte(key)) {
		return nil, errInvalidHMAC
	}

	return data, nil
}

const (
	githubWebhookSecretName = "github-webhook"
	xHubSignature256Name    = "X-Hub-Signature-256"
)

var (
	errNoKey           = fmt.Errorf("no secret for %q", githubWebhookSecretName)
	errNoPayload       = errors.New("empty payload")
	errInvalidHMAC     = errors.New("invalid HMAC")
	errNoHeader        = fmt.Errorf("missing %q header entry", xHubSignature256Name)
	errMalformedHeader = fmt.Errorf("malformed %q header entry", xHubSignature256Name)
)

// parseMAC reads the value of the SHA-256 HMAC tag from the
// X-Hub-Signature-256 header entry of h, which must be of the
// form "sha256=HMAC", where HMAC is a hex-encoded HMAC tag.
// It returns an error if the header entry is not present or malformed.
func parseMAC(h *http.Header) ([]byte, error) {
	entry := h.Get(xHubSignature256Name)
	if entry == "" {
		return nil, errNoHeader
	}
	hexMAC, ok := strings.CutPrefix(entry, "sha256=")
	if !ok {
		return nil, errMalformedHeader
	}
	return hex.DecodeString(hexMAC)
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
// payload is the body of the returned request.
//
// For testing.
func ValidWebhookTestdata(t *testing.T, payload string) (*http.Request, secret.DB) {
	key := "test-key"
	signature := computeXHubSignature256(t, payload, key)
	return newWebhookRequest(t, signature, payload), newWebhookSecretDB(t, key)
}

// computeXHubSignature256 returns the expected value of the
// X-Hub-Signature-256 header entry in a GitHub webhook request, of the
// form "sha256=HMAC" where HMAC is the hex-encoded SHA-256 HMAC tag of
// the given payload created with key.
//
// For testing.
func computeXHubSignature256(t *testing.T, payload, key string) string {
	t.Helper()

	h := computeMAC([]byte(payload), []byte(key))
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
func newWebhookRequest(t *testing.T, xHubSignature256, payload string) *http.Request {
	t.Helper()

	r, err := http.NewRequest(http.MethodPost, "", strings.NewReader(payload))
	if err != nil {
		t.Fatal("could not create test request")
	}
	if xHubSignature256 != "" {
		r.Header.Set(xHubSignature256Name, xHubSignature256)
	}
	return r
}
