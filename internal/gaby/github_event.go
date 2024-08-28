// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oscar/internal/github"
)

// handleGitHubEvent takes action when an event occurs on GitHub.
// Currently, the function only logs that it was able to validate
// and parse the request.
func (g *Gaby) handleGitHubEvent(r *http.Request) error {
	payload, err := github.ValidateWebhookRequest(r, g.secret)
	if err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}

	var event map[string]any
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("could not unmarshal payload: %w", err)
	}

	g.slog.Info("new GitHub event", "event", event)
	return nil
}
