// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package discussion

import (
	"time"

	gql "github.com/shurcooL/githubv4"
	"golang.org/x/oscar/internal/github"
)

// query is a query to the GitHub GraphQL API.
type query[N node] interface {
	// Page returns the current page.
	Page() page[N]
	// CursorName returns the name of the cursor variable for this query.
	CursorName() string
}

// page is a single page in a query to the GitHub GraphQL API.
type page[N node] struct {
	Nodes    []N
	PageInfo struct {
		EndCursor   gql.String
		HasNextPage bool
	}
}

// connection is a connection to another data source
// in the GitHub GraphQL API.
// e.g. https://docs.github.com/en/graphql/reference/objects#discussioncommentconnection
type connection[N any] struct {
	page[N]
	TotalCount gql.Int
}

// node is a data node in a query to the GitHub GraphQL API.
type node any

// max number of items per page allowed by GitHub
const githubPageLimit = 100

// max number of items per page
// (may be modified for testing).
var maxItemsPerPage = githubPageLimit

// The "vars" input to [gql.Query].
type varsMap map[string]any

// The key names for maps of type [varsMap].
const (
	discCursor  = "cursor"
	discPerPage = "perPage"
	orderBy     = "orderBy"
	ownerKey    = "owner"
	repoKey     = "repo"

	nodeId = "node"

	discNumber      = "number"
	commentsCursor  = "commentsCursor"
	commentsPerPage = "commentsPerPage"
	repliesCursor   = "repliesCursor"
	repliesPerPage  = "repliesPerPage"

	labelsCursor  = "labelsCursor"
	labelsPerPage = "labelsPerPage"
)

var _ query[*discussion] = (*listQuery)(nil)

// listQuery is a query to list dicussions for a repo.
// https://docs.github.com/en/graphql/guides/using-the-graphql-api-for-discussions#repositorydiscussions
type listQuery struct {
	Repository struct {
		Discussions page[*discussion] `graphql:"discussions(first: $perPage, after: $cursor, orderBy: $orderBy)"`
	} `graphql:"repository(owner: $owner, name: $repo)"`
}

// newListQuery returns a query and vars to input to [gql.Query],
// in order to list discussions for the given project.
func newListQuery(owner, repo string) (*listQuery, varsMap) {
	labelsPerPageValue := min(maxItemsPerPage, 10)
	q := &listQuery{}
	return q, varsMap{
		ownerKey:      gql.String(owner),
		repoKey:       gql.String(repo),
		discCursor:    (*gql.String)(nil),
		labelsCursor:  (*gql.String)(nil),
		discPerPage:   gql.Int(maxItemsPerPage),
		labelsPerPage: (gql.Int)(labelsPerPageValue),
		orderBy: gql.DiscussionOrder{
			Field:     gql.DiscussionOrderFieldUpdatedAt,
			Direction: gql.OrderDirectionDesc,
		},
	}
}

// Page implements [query.Page].
func (q *listQuery) Page() page[*discussion] {
	return q.Repository.Discussions
}

// CursorName implements [query.CursorName].
func (*listQuery) CursorName() string {
	return discCursor
}

var _ query[*discWithComments] = (*listDiscWithCommentsQuery)(nil)

// listDiscWithCommentsQuery is a query to list all dicussions for a repo, with
// a focus on accessing comments instead of the discussions themselves.
// https://docs.github.com/en/graphql/guides/using-the-graphql-api-for-discussions#repositorydiscussions
type listDiscWithCommentsQuery struct {
	Repository struct {
		Discussions page[*discWithComments] `graphql:"discussions(first: $perPage, after: $cursor, orderBy: $orderBy)"`
	} `graphql:"repository(owner: $owner, name: $repo)"`
}

// newListQuery returns a query and vars to input to [gql.Query],
// in order to list all discussions and comments for the given project.
func newListDiscWithCommentsQuery(owner, repo string) (*listDiscWithCommentsQuery, varsMap) {
	// The API times out if we try to request too many nodes at once,
	// so restrict the number of items per page values on the first query.
	dpp := min(maxItemsPerPage, 20)
	cpp := min(maxItemsPerPage, 10)
	rpp := min(maxItemsPerPage, 10)
	q := &listDiscWithCommentsQuery{}
	return q, varsMap{
		ownerKey:       gql.String(owner),
		repoKey:        gql.String(repo),
		discCursor:     (*gql.String)(nil),
		commentsCursor: (*gql.String)(nil),
		repliesCursor:  (*gql.String)(nil),
		orderBy: gql.DiscussionOrder{
			Field:     gql.DiscussionOrderFieldUpdatedAt,
			Direction: gql.OrderDirectionDesc,
		},
		discPerPage:     (gql.Int)(dpp),
		commentsPerPage: (gql.Int)(cpp),
		repliesPerPage:  (gql.Int)(rpp),
	}
}

// Page implements [query.Page].
func (q *listDiscWithCommentsQuery) Page() page[*discWithComments] {
	return q.Repository.Discussions
}

// CursorName implements [query.CursorName].
func (*listDiscWithCommentsQuery) CursorName() string {
	return discCursor
}

var _ query[*comment] = (*listCommentsQuery)(nil)

// listCommentsQuery is a query to list all comments for a discussion.
// https://docs.github.com/en/graphql/guides/using-the-graphql-api-for-discussions#repositorydiscussion
type listCommentsQuery struct {
	Repository struct {
		Discussion discWithComments `graphql:"discussion(number: $number)"`
	} `graphql:"repository(owner: $owner, name: $repo)"`
}

// newListCommentsQuery returns a query and vars to input to [gql.Query],
// in order to list all comments for a single discussion in the given project.
// number is the discussion number, and cursor is the comments cursor to use
// in the initial query.
func newListCommentsQuery(owner, repo string, number gql.Int, cursor gql.String) (*listCommentsQuery, varsMap) {
	return &listCommentsQuery{}, varsMap{
		ownerKey:        gql.String(owner),
		repoKey:         gql.String(repo),
		discNumber:      number,
		commentsCursor:  cursor,
		repliesCursor:   (*gql.String)(nil),
		commentsPerPage: gql.Int(maxItemsPerPage),
		repliesPerPage:  gql.Int(maxItemsPerPage),
	}
}

// Page implements [query.Page].
func (q *listCommentsQuery) Page() page[*comment] {
	return q.Repository.Discussion.Comments.page
}

// CursorName implements [query.CursorName].
func (*listCommentsQuery) CursorName() string {
	return commentsCursor
}

var _ query[*reply] = (*commentQuery)(nil)

// commentQuery is a query to get a specific discussion comment,
// with a focus on accessing its replies.
type commentQuery struct {
	Node struct {
		Comment comment `graphql:"... on DiscussionComment"`
	} `graphql:"node(id: $node)"`
}

// newCommentQuery returns a query and vars to input to [gql.Query],
// in order to get a single comment by node ID.
// cursor is the replies cursor to use in the initial query.
func newCommentQuery(nodeID gql.ID, cursor gql.String) (*commentQuery, varsMap) {
	q := &commentQuery{}
	return q, varsMap{
		nodeId:         nodeID,
		repliesCursor:  cursor,
		repliesPerPage: gql.Int(maxItemsPerPage),
	}
}

// Page implements [query.Page].
func (q *commentQuery) Page() page[*reply] {
	return q.Node.Comment.Replies.page
}

// CursorName implements [query.CursorName].
func (*commentQuery) CursorName() string {
	return repliesCursor
}

// discWithComments is minimal representation of a discussion used
// to query for comments.
type discWithComments struct {
	URL       gql.URI
	Number    gql.Int
	UpdatedAt gql.DateTime
	Comments  connection[*comment] `graphql:"comments(first: $commentsPerPage, after: $commentsCursor)"`
}

// A discussion is a GitHub discussion, as returned by the GitHub
// GraphQL API.
// https://docs.github.com/en/graphql/reference/objects#discussion
type discussion struct {
	ActiveLockReason  *gql.LockReason
	IsAnswered        gql.Boolean
	Answer            *commentRef
	AnswerChosenAt    *gql.DateTime
	Author            actor
	AuthorAssociation *gql.CommentAuthorAssociation
	Body              gql.String // markdown
	Category          discussionCategory
	ClosedAt          gql.DateTime
	CreatedAt         gql.DateTime
	ID                gql.ID
	Labels            connection[label] `graphql:"labels(first:$labelsPerPage, after:$labelsCursor)"`
	LastEditedAt      *gql.DateTime
	Locked            gql.Boolean
	Number            gql.Int
	ResourcePath      gql.String
	Title             gql.String
	UpdatedAt         gql.DateTime
	UpvoteCount       gql.Int
	URL               gql.URI
}

// A label is a GitHub label, as returned by the GitHub
// GraphQL API.
// https://docs.github.com/en/graphql/reference/objects#label
type label struct {
	Name gql.String
}

// convert converts the GitHub GraphQL representation of
// a discussion to a format intended to be stored in a database.
func (d *discussion) convert() *Discussion {
	lastEditedAt, activeLockReason := "", ""
	if d.LastEditedAt != nil {
		lastEditedAt = timeToStr(*d.LastEditedAt)
	}
	if d.ActiveLockReason != nil {
		activeLockReason = string(*d.ActiveLockReason)
	}
	return &Discussion{
		URL:              string(d.URL.String()),
		Number:           int64(d.Number),
		Author:           d.Author.convert(),
		Title:            string(d.Title),
		CreatedAt:        timeToStr(d.CreatedAt),
		UpdatedAt:        timeToStr(d.UpdatedAt),
		LastEditedAt:     lastEditedAt,
		Body:             string(d.Body),
		UpvoteCount:      int(d.UpvoteCount),
		Locked:           bool(d.Locked),
		ActiveLockReason: activeLockReason,
		Labels:           toLabels(d.Labels),
	}
}

// toLabels converts the labels on the first page
// to a list of [Label]s.
// It does not page through the labels.
func toLabels(ls connection[label]) []github.Label {
	var labels []github.Label
	for _, n := range ls.Nodes {
		labels = append(labels, github.Label{Name: string(n.Name)})
	}
	return labels
}

// discussionRef is a minimal representation of a discussion,
// used to refer back to a discussion (to avoid infinite recursion).
type discussionRef struct {
	Number    gql.Int
	UpdatedAt gql.DateTime
	URL       gql.URI
}

// actor is a GitHub user or organization.
// https://docs.github.com/en/graphql/reference/interfaces#actor
type actor struct {
	Login gql.String
}

// convert converts the GitHub GraphQL representation of
// an actor to a format intended to be stored in a database.
func (a *actor) convert() github.User {
	return github.User{
		Login: string(a.Login),
	}
}

// discussionCategory is a GitHub discussion category.
// https://docs.github.com/en/graphql/reference/objects#discussioncategory
type discussionCategory struct {
	Name gql.String
}

// comment is the GitHub GraphQL representation of a discussion comment
// with replies.
// https://docs.github.com/en/graphql/reference/objects#discussioncomment
type comment struct {
	commentBase
	Replies connection[*reply] `graphql:"replies(first: $repliesPerPage, after: $repliesCursor)"`
}

// comment is the GitHub GraphQL representation of a discussion comment
// that is itself a reply to another comment.
// https://docs.github.com/en/graphql/reference/objects#discussioncomment
type reply struct {
	commentBase
	ReplyTo *commentRef
}

// commentBase is the data that [comment] and [reply] have in common.
// https://docs.github.com/en/graphql/reference/objects#discussioncomment
type commentBase struct {
	Author              actor
	AuthorAssociation   gql.CommentAuthorAssociation
	Body                gql.String // markdown
	CreatedAt           gql.DateTime
	DeletedAt           *gql.DateTime
	Discussion          *discussionRef
	Editor              *actor
	ID                  gql.ID
	IncludesCreatedEdit gql.Boolean
	IsAnswer            gql.Boolean
	IsMinimized         gql.Boolean
	LastEditedAt        *gql.DateTime
	MinimizedReason     *gql.String
	PublishedAt         *gql.DateTime
	UpdatedAt           gql.DateTime
	UpvoteCount         gql.Int
	URL                 gql.URI
}

// convert converts the GitHub GraphQL representation of
// a comment to a format intended to be stored in a database.
func (c *commentBase) convert() *Comment {
	return &Comment{
		URL:           c.URL.String(),
		DiscussionURL: c.Discussion.URL.String(),
		Author:        c.Author.convert(),
		CreatedAt:     timeToStr(c.CreatedAt),
		UpdatedAt:     timeToStr(c.UpdatedAt),
		Body:          string(c.Body),
	}
}

func timeToStr(t gql.DateTime) string {
	return t.Format(time.RFC3339)
}

// convert converts the GitHub GraphQL representation of
// a reply to a format intended to be stored in a database.
func (r *reply) convert() *Comment {
	c := r.commentBase.convert()
	c.ReplyToURL = r.ReplyTo.URL.String()
	return c
}

// commentRef is a minimal representation of a discussion comment, used
// to refer back to a comment (to avoid infinite recursion).
type commentRef struct {
	ID  gql.ID
	URL gql.URI
}
