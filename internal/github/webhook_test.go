// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"bytes"
	"errors"
	"testing"

	"golang.org/x/oscar/internal/secret"
)

func TestValidateWebhookRequest(t *testing.T) {
	// Example test case from GitHub
	// (https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries#testing-the-webhook-payload-validation).
	defaultHeaderEntry := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	defaultPayload := "Hello, World!"
	defaultKey := "It's a Secret to Everybody"

	t.Run("success", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			headerEntry string
			payload     string
			key         string
		}{
			{
				name:        "hardcoded",
				headerEntry: defaultHeaderEntry,
				payload:     defaultPayload,
				key:         defaultKey,
			},
			{
				name:        "computed",
				headerEntry: computeXHubSignature256(t, "a payload", "a key"),
				payload:     "a payload",
				key:         "a key",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r := newWebhookRequest(t, tc.headerEntry, tc.payload)
				db := newWebhookSecretDB(t, tc.key)

				got, err := ValidateWebhookRequest(r, db)
				if err != nil {
					t.Fatalf("ValidateWebhookRequest err = %s, want nil", err)
				}
				want := []byte(tc.payload)
				if !bytes.Equal(got, want) {
					t.Errorf("ValidateWebhookRequest = %q, want %q", got, want)
				}
			})
		}
	})

	t.Run("error", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			headerEntry string
			payload     string
			key         string
			wantErr     error
		}{
			{
				name:        "no X-Hub-Signature-256 header entry",
				headerEntry: "",
				payload:     defaultPayload,
				key:         defaultKey,
				wantErr:     errNoHeader,
			},
			{
				name:        "malformed X-Hub-Signature-256 header entry",
				headerEntry: "sha=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17",
				payload:     defaultPayload,
				key:         defaultKey,
				wantErr:     errMalformedHeader,
			},
			{
				name:        "wrong payload",
				headerEntry: defaultHeaderEntry,
				payload:     "a different payload",
				key:         defaultKey,
				wantErr:     errInvalidHMAC,
			},
			{
				name:        "wrong key",
				headerEntry: defaultHeaderEntry,
				payload:     defaultPayload,
				key:         "a different key",
				wantErr:     errInvalidHMAC,
			},
			{
				name:        "no key",
				headerEntry: defaultHeaderEntry,
				payload:     defaultPayload,
				key:         "",
				wantErr:     errNoKey,
			},
			{
				name:        "no payload",
				headerEntry: defaultHeaderEntry,
				payload:     "",
				key:         defaultKey,
				wantErr:     errNoPayload,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r := newWebhookRequest(t, tc.headerEntry, tc.payload)
				db := secret.Empty()
				if tc.key != "" {
					db = newWebhookSecretDB(t, tc.key)
				}

				_, err := ValidateWebhookRequest(r, db)
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("ValidateWebhookRequest err = %v, want error %v", err, tc.wantErr)
				}
			})
		}
	})
}
