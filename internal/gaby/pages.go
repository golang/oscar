// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// The browsable Gaby webpages.
// Pages listed here will appear in navigation.
var pages = []pageID{
	// Dev pages.
	actionlogID, dbviewID, bisectlogID,
	// User pages.
	overviewID, searchID, rulesID, labelsID,
	// reviews omitted for now, as it loads very slowly
}

// Gaby webpage endpoints.
const (
	actionlogID pageID = "actionlog"
	overviewID  pageID = "overview"
	searchID    pageID = "search"
	dbviewID    pageID = "dbview"
	rulesID     pageID = "rules"
	labelsID    pageID = "labels"
	reviewsID   pageID = "reviews"
	bisectlogID pageID = "bisectlog"
)

// Gaby webpage titles.
var titles = map[pageID]string{
	actionlogID: "Action Log",
	overviewID:  "Overviews",
	searchID:    "Search",
	dbviewID:    "Database Viewer",
	rulesID:     "Rule Checker",
	reviewsID:   "Reviews",
	labelsID:    "Issue Labels",
	bisectlogID: "Bisect Log",
}
