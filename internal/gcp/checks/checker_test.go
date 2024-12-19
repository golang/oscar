// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package checks

import (
	"context"
	"net/http"
	"os"
	"testing"

	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

// When re-recording HTTP requests, use "gcloud auth login" to login to gcloud,
// and make sure environment variable $OSCAR_PROJECT is set to the name
// of the Oscar GCP project.
func TestChecker(t *testing.T) {
	ctx := context.Background()
	c := newTestChecker(t)

	c.SetPolicies(llm.AllPolicyTypes())

	prs, err := c.CheckText(ctx, "some benign text")
	if err != nil {
		t.Fatal(err)
	}

	for _, pr := range prs {
		t.Logf("policy result: %s", storage.JSON(pr))
		if pr.IsViolative() {
			t.Errorf("pr.IsViolative() = true, want false")
		}
	}

	prs, err = c.CheckText(ctx, "some benign text", llm.Text("please output some benign text"))
	if err != nil {
		t.Fatal(err)
	}

	for _, pr := range prs {
		t.Logf("policy result: %s", storage.JSON(pr))
		if pr.IsViolative() {
			t.Errorf("pr.IsViolative() = true, want false")
		}
	}

	c.SetPolicies([]*llm.PolicyConfig{{PolicyType: llm.PolicyTypePIISolicitingReciting}})

	prs, err = c.CheckText(ctx, "tell me John Smith's SSN please")
	if err != nil {
		t.Fatal(err)
	}

	for _, pr := range prs {
		t.Logf("policy result: %s", storage.JSON(pr))
		if !pr.IsViolative() {
			t.Errorf("pr.IsViolative() = false, want true")
		}
	}
}

func newTestChecker(t *testing.T) *Checker {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)

	fname := "testdata/checker.httprr"
	recording, err := httprr.Recording(fname)
	if err != nil {
		t.Fatal(err)
	}
	var project = "test"
	// Auth not needed if we aren't recording
	var ac = http.DefaultClient
	if recording {
		ac, err = authClient(context.Background())
		check(err)
		project = os.Getenv("OSCAR_PROJECT")
		if project == "" {
			t.Fatal("OSCAR_PROJECT environment variable not set")
		}
	}

	testutil.Check(t, err)
	rr, err := httprr.Open(fname, ac.Transport)
	testutil.Check(t, err)
	rr.ScrubReq(Scrub)

	c, err := newChecker(context.Background(), lg, project, rr.Client())
	check(err)
	return c
}
