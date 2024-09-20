// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"golang.org/x/oscar/internal/secret"
)

func TestValidateWebhookRequest(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		for _, tc := range []struct {
			event   WebhookEventType
			payload any
		}{
			{
				event: WebhookEventTypeIssue,
				payload: &WebhookIssueEvent{
					Action: WebhookIssueActionOpened,
					Repository: Repository{
						Project: "a/project",
					},
				},
			},
			{
				event: WebhookEventTypeIssueComment,
				payload: &WebhookIssueCommentEvent{
					Action: WebhookIssueCommentActionCreated,
					Repository: Repository{
						Project: "a/project",
					},
				},
			},
			{
				event:   WebhookEventType("other"),
				payload: json.RawMessage([]byte(`{"hello":"world"}`)),
			},
		} {
			t.Run(string(tc.event), func(t *testing.T) {
				r, db := ValidWebhookTestdata(t, tc.event, tc.payload)
				got, err := ValidateWebhookRequest(r, db)
				if err != nil {
					t.Fatalf("ValidateWebhookRequest err = %s, want nil", err)
				}
				want := &WebhookEvent{Type: tc.event, Payload: tc.payload}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("ValidateWebhookRequest = %s, want %s", got, want)
				}
			})
		}
	})

	t.Run("error", func(t *testing.T) {
		key := "test-key"
		db := newWebhookSecretDB(t, key)

		defaultProject := "a/project"
		defaultEvent := WebhookEventTypeIssue
		defaultPayload, err := json.Marshal(WebhookIssueEvent{
			Action: WebhookIssueActionOpened,
			Repository: Repository{
				Project: defaultProject,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defaultSignature := computeXHubSignature256(t, defaultPayload, key)

		getRequest, err := http.NewRequest(http.MethodGet, "", nil)
		if err != nil {
			t.Fatal(err)
		}

		for _, tc := range []struct {
			name    string
			r       *http.Request
			wantErr error
		}{
			{
				name:    "wrong method",
				r:       getRequest,
				wantErr: errBadHTTPMethod,
			},
			{
				name:    "no payload",
				r:       newWebhookRequest(t, defaultEvent, defaultSignature, nil),
				wantErr: errNoPayload,
			},
			{
				name:    "no event",
				r:       newWebhookRequest(t, "", defaultSignature, defaultPayload),
				wantErr: errNoEventType,
			},
			{
				name:    "invalid signature",
				r:       newWebhookRequest(t, defaultEvent, "sha256=deadbeef", defaultPayload),
				wantErr: errInvalidHMAC,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := ValidateWebhookRequest(tc.r, db)
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("ValidateWebhookRequest err = %v, want error %v", err, tc.wantErr)
				}
			})
		}
	})
}

func TestValidateWebhookSignature(t *testing.T) {
	// Example test case from GitHub
	// (https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries#testing-the-webhook-payload-validation).
	defaultHeaderEntry := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	defaultPayload := []byte("Hello, World!")
	defaultKey := "It's a Secret to Everybody"

	t.Run("success", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			signature string
			payload   []byte
			key       string
		}{
			{
				name:      "hardcoded",
				signature: defaultHeaderEntry,
				payload:   defaultPayload,
				key:       defaultKey,
			},
			{
				name:      "computed",
				signature: computeXHubSignature256(t, []byte("a payload"), "a key"),
				payload:   []byte("a payload"),
				key:       "a key",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				db := newWebhookSecretDB(t, tc.key)

				err := validateWebhookSignature(tc.payload, tc.signature, db)
				if err != nil {
					t.Fatalf("validateWebhookSignature err = %s, want nil", err)
				}
			})
		}
	})

	t.Run("error", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			signature string
			payload   []byte
			key       string
			wantErr   error
		}{
			{
				name:      "no X-Hub-Signature-256 header entry",
				signature: "",
				payload:   defaultPayload,
				key:       defaultKey,
				wantErr:   errNoSignatureHeader,
			},
			{
				name:      "malformed X-Hub-Signature-256 header entry",
				signature: "sha=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17",
				payload:   defaultPayload,
				key:       defaultKey,
				wantErr:   errBadSignatureHeader,
			},
			{
				name:      "wrong payload",
				signature: defaultHeaderEntry,
				payload:   []byte("a different payload"),
				key:       defaultKey,
				wantErr:   errInvalidHMAC,
			},
			{
				name:      "wrong key",
				signature: defaultHeaderEntry,
				payload:   defaultPayload,
				key:       "a different key",
				wantErr:   errInvalidHMAC,
			},
			{
				name:      "no key",
				signature: defaultHeaderEntry,
				payload:   defaultPayload,
				key:       "",
				wantErr:   errNoKey,
			},
			{
				name:      "no payload",
				signature: defaultHeaderEntry,
				payload:   nil,
				key:       defaultKey,
				wantErr:   errInvalidHMAC,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				db := secret.Empty()
				if tc.key != "" {
					db = newWebhookSecretDB(t, tc.key)
				}

				err := validateWebhookSignature(tc.payload, tc.signature, db)
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("validateWebhookSignature err = %v, want error %v", err, tc.wantErr)
				}
			})
		}
	})
}
