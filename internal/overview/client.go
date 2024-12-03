// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package overview generates and posts overviews of discussions.
// For now, it only works with GitHub issues and their comments.
// TODO(tatianabradley): Implement posting logic.
package overview

import (
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llmapp"
)

type Client struct {
	lc *llmapp.Client
	gh *github.Client
}

// New returns a new Client used to generate and post overviews.
func New(lc *llmapp.Client, gh *github.Client) *Client {
	return &Client{
		lc: lc,
		gh: gh,
	}
}
