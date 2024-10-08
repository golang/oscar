// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"net/http"

	"golang.org/x/oscar/internal/reviews"
)

func (g *Gaby) handleReviewDashboard(w http.ResponseWriter, r *http.Request) {
	// TODO: pass change list and score predicates
	reviews.Display(g.slog, "", nil, w, r)
}
