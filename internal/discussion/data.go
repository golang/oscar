// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package discussion

import (
	"golang.org/x/oscar/internal/github"
)

// Discussion represents a GitHub discussion and its metadata.
type Discussion struct {
	URL              string         `json:"url"`
	Number           int64          `json:"number"`
	Author           github.User    `json:"author"`
	Title            string         `json:"title"`
	CreatedAt        string         `json:"created_at"`
	UpdatedAt        string         `json:"updated_at"`
	LastEditedAt     string         `json:"last_edited_at,omitempty"`
	ClosedAt         string         `json:"closed_at"`
	Body             string         `json:"body"`
	UpvoteCount      int            `json:"upvote_count"`
	Locked           bool           `json:"locked"`
	ActiveLockReason string         `json:"active_lock_reason,omitempty"`
	Labels           []github.Label `json:"labels"`
}

// Comment represents a GitHub discussion comment and its metadata.
type Comment struct {
	// URL of this comment.
	URL string `json:"url"`
	// URL of the discussion this is a comment on
	DiscussionURL string `json:"discussion_url"`
	// URL of the comment this is a reply to, if applicable
	ReplyToURL string      `json:"reply_to_url,omitempty"`
	Author     github.User `json:"author"`
	CreatedAt  string      `json:"created_at"`
	UpdatedAt  string      `json:"updated_at"`
	Body       string      `json:"body"`
}

// ID returns the numerical ID of a comment (the last part of its URL),
// or 0 if its URL is not valid.
func (c *Comment) ID() int64 {
	n, err := parseID(c.URL, "-")
	if err != nil {
		return 0
	}
	return n
}
