// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"net/http"

	"golang.org/x/oscar/internal/goreviews"
)

func (g *Gaby) handleReviewDashboard(w http.ResponseWriter, r *http.Request) {
	c := goreviews.New(g.slog, g.gerrit, g.gerritProjects)
	if err := c.Sync(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	c.Display(w, r)
}
