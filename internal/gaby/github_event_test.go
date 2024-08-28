// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"log/slog"
	"testing"

	"golang.org/x/oscar/internal/github"
)

func TestHandleGitHubEvent(t *testing.T) {
	validPayload := `{"number":1}`
	r, db := github.ValidWebhookTestdata(t, validPayload)
	g := &Gaby{secret: db, slog: slog.Default()}
	if err := g.handleGitHubEvent(r); err != nil {
		t.Fatalf("handleGitHubEvent err = %v, want nil", err)
	}

	invalidPayload := "not JSON"
	r2, db2 := github.ValidWebhookTestdata(t, invalidPayload)
	g2 := &Gaby{secret: db2, slog: slog.Default()}
	if err := g2.handleGitHubEvent(r2); err == nil {
		t.Fatal("handleGitHubEvent err = nil, want err")
	}
}
