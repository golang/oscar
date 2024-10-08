// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package discussion

import (
	"context"
	"iter"
	"log/slog"
	"net/http"

	gql "github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/secret"
)

type gqlClient struct {
	slog *slog.Logger
	gql.Client
}

func newGQLClient(hc *http.Client) *gqlClient {
	return &gqlClient{
		Client: *gql.NewClient(hc),
		slog:   slog.Default(),
	}
}

func authClient(ctx context.Context, sdb secret.DB) *http.Client {
	return oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: github.Token(sdb),
	}))
}

// discussions returns an iterator over all discussions in the project,
// ordered by update time (latest first).
// It returns an error if any of the GitHub queries fails.
func (gc *gqlClient) discussions(ctx context.Context, owner, repo string) iter.Seq2[*Discussion, error] {
	return func(yield func(*Discussion, error) bool) {
		q, vars := newListQuery(owner, repo)
		for d, err := range nodes(ctx, gc, q, vars) {
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(d.convert(), nil) {
				return
			}
		}
	}
}

// comments returns an iterator over all discussion comments and replies in the project.
// The order is not guaranteed because ordering of comments and replies can't be configured
// via the GitHub GraphQL API.
// The function returns an error if any of the GitHub queries fails.
func (gc *gqlClient) comments(ctx context.Context, owner, repo string) iter.Seq2[*Comment, error] {
	yieldCommentAndReplies := func(c *comment, yield func(*Comment, error) bool) bool {
		if !yield(c.convert(), nil) {
			return false
		}
		for r, err := range gc.replies(ctx, c) {
			if err != nil {
				return yield(nil, err)
			}
			if !yield(r.convert(), nil) {
				return false
			}
		}
		return true
	}

	return func(yield func(*Comment, error) bool) {
		q, vars := newListDiscWithCommentsQuery(owner, repo)
		for d, err := range nodes(ctx, gc, q, vars) {
			if err != nil {
				yield(nil, err)
				return
			}
			for _, c := range d.Comments.Nodes {
				if !yieldCommentAndReplies(c, yield) {
					return
				}
			}
			if d.Comments.PageInfo.HasNextPage {
				q, vars := newListCommentsQuery(owner, repo, d.Number, d.Comments.PageInfo.EndCursor)
				for c, err := range nodes(ctx, gc, q, vars) {
					if err != nil {
						yield(nil, err)
						return
					}
					if !yieldCommentAndReplies(c, yield) {
						return
					}
				}
			}
		}
	}
}

// replies returns an iterator over the replies to the given comment.
// The order is not guaranteed. It returns an error if any of the GitHub queries fails.
func (gc *gqlClient) replies(ctx context.Context, c *comment) iter.Seq2[*reply, error] {
	return func(yield func(*reply, error) bool) {
		replies := c.Replies
		for _, r := range replies.Nodes {
			if !yield(r, nil) {
				return
			}
		}
		if replies.PageInfo.HasNextPage {
			q, vars := newCommentQuery(c.ID, replies.PageInfo.EndCursor)
			for r, err := range nodes(ctx, gc, q, vars) {
				if err != nil {
					yield(nil, err)
					return
				}
				if !yield(r, nil) {
					return
				}
			}
		}
	}
}

// nodes returns an iterator over the nodes of the query.
// q and vars are the initial inputs to [gql.Client.Query].
// It returns an error if any of the GitHub queries fails.
// TODO(tatianabradley): Add a check to see if we have hit a GitHub
// rate limit and slow down if so.
func nodes[N node, Q query[N]](ctx context.Context, gc *gqlClient, q Q, vars varsMap) iter.Seq2[N, error] {
	return func(yield func(N, error) bool) {
		var zero N
		for {
			if err := gc.Query(ctx, q, vars); err != nil {
				yield(zero, err)
				return
			}
			page := q.Page()
			for _, n := range page.Nodes {
				if !yield(n, nil) {
					return
				}
			}
			if !page.PageInfo.HasNextPage {
				return
			}
			vars[q.CursorName()] = gql.NewString(page.PageInfo.EndCursor)
		}
	}
}
