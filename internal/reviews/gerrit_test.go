// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reviews

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"golang.org/x/oscar/internal/gerrit"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

// testGerritClient returns a [*gerrit.Client] for testing.
func testGerritClient(t *testing.T) *gerrit.Client {
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	sdb := secret.Empty()
	return gerrit.New("reviews-test", lg, db, sdb, nil)
}

// loadTestChange loads a txtar file as a [Change].
func loadTestChange(t *testing.T, gc *gerrit.Client, filename string, num int) Change {
	tc := gc.Testing()
	testutil.Check(t, tc.LoadTxtar(filename))
	gch := gc.Change("test", num)
	grc := &GerritReviewClient{
		GClient:  gc,
		Accounts: testAccounts(),
	}
	change := &GerritChange{
		Client: grc,
		Change: gch,
	}
	return change
}

func TestGerritChange(t *testing.T) {
	gc := testGerritClient(t)
	change := loadTestChange(t, gc, "testdata/gerritchange.txt", 1)

	toEmail := func(fn func(ctx context.Context) []Account) func(context.Context) []string {
		return func(ctx context.Context) []string {
			var ret []string
			for _, r := range fn(ctx) {
				ret = append(ret, r.Name(ctx))
			}
			slices.Sort(ret)
			return ret
		}
	}

	ctx := context.Background()
	tests := []struct {
		name   string
		method func(context.Context) any
		want   any
	}{
		{
			"ID",
			wm(change.ID),
			"1",
		},
		{
			"Status",
			wm(change.Status),
			StatusReady,
		},
		{
			"Author",
			wm(change.Author(ctx).Name),
			"gopher@golang.org",
		},
		{
			"Created",
			wm(change.Created),
			time.Date(2024, 10, 1, 10, 10, 10, 0, time.UTC),
		},
		{
			"Updated",
			wm(change.Updated),
			time.Date(2024, 10, 3, 10, 10, 10, 0, time.UTC),
		},
		{
			"UpdatedByAuthor",
			wm(change.UpdatedByAuthor),
			time.Date(2024, 10, 2, 10, 10, 10, 0, time.UTC),
		},
		{
			"Subject",
			wm(change.Subject),
			"my new change",
		},
		{
			"Description",
			wm(change.Description),
			"initial change",
		},
		{
			"Reviewers",
			wm(toEmail(change.Reviewers)),
			[]string{
				"maintainer@golang.org",
			},
		},
		{
			"Reviewed",
			wm(toEmail(change.Reviewed)),
			[]string{
				"commenter@golang.org",
				"maintainer@golang.org",
			},
		},
		{
			"Needs",
			wm(change.Needs),
			Needs(0),
		},
	}

	for _, test := range tests {
		got := test.method(ctx)
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s got %v, want %v", test.name, got, test.want)
		}
	}

	gerritChanges := func(yield func(*gerrit.Change) bool) {
		for _, changeFn := range gc.ChangeNumbers("test") {
			if !yield(changeFn()) {
				return
			}
		}
	}

	it := GerritChanges(gc, testAccounts(), gerritChanges)
	got := slices.Collect(it)
	var gotIDs []string
	for _, g := range got {
		gotIDs = append(gotIDs, g.ID(ctx))
	}
	slices.Sort(gotIDs)
	wantIDs := []string{"1", "2"}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Errorf("GerritChanges returned IDs %v, want %v", gotIDs, wantIDs)
	}
}

// changeMethod is one of the methods used to retrieve Change values.
type changeMethod[T any] func(context.Context) T

// wm wraps a changeMethod in a function that we can put in a table.
func wm[T any](fn changeMethod[T]) func(ctx context.Context) any {
	return func(ctx context.Context) any {
		return fn(ctx)
	}
}

// gerritTestAccount implements [Account].
type gerritTestAccount struct {
	name        string
	displayName string
	authority   Authority
	commits     int
}

// Name implements Account.Name.
func (gta *gerritTestAccount) Name(ctx context.Context) string {
	return gta.name
}

// DisplayName implements Account.DisplayName.
func (gta *gerritTestAccount) DisplayName(ctx context.Context) string {
	return gta.displayName
}

// Authority implements Account.Authority
func (gta *gerritTestAccount) Authority(ctx context.Context) Authority {
	return gta.authority
}

// Commits implements Account.Commits.
func (gta *gerritTestAccount) Commits(ctx context.Context) int {
	return gta.commits
}

// gerritTestAccountLookup implements [AccountLookup].
type gerritTestAccountLookup map[string]Account

func (gtal gerritTestAccountLookup) Lookup(ctx context.Context, name string) Account {
	return gtal[name]
}

// testAccounts returns an implementation of [AccountLookup] for testing.
var testAccounts = sync.OnceValue(func() AccountLookup {
	return gerritTestAccountLookup{
		"gopher@golang.org": &gerritTestAccount{
			name:        "gopher@golang.org",
			displayName: "gopher",
			authority:   AuthorityReviewer,
			commits:     10,
		},
		"maintainer@golang.org": &gerritTestAccount{
			name:        "maintainer@golang.org",
			displayName: "maintainer",
			authority:   AuthorityMaintainer,
			commits:     10,
		},
		"commenter@golang.org": &gerritTestAccount{
			name:        "commenter@golang.org",
			displayName: "commenter",
			authority:   AuthorityContributor,
			commits:     10,
		},
	}
})
