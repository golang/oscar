// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package overview

import "errors"

// TODO(tatianabradley): Implement.
type poster struct {
	name            string // the name of the poster (for internal state)
	bot             string // the name of the GitHub bot that will post
	minComments     int
	requireApproval bool
	projects        map[string]bool
}

// TODO(tatianabradley): Implement.
func newPoster(name, bot string) *poster {
	return &poster{name: name, bot: bot}
}

// TODO(tatianabradley): Implement.
func (*poster) run() error {
	return errors.New("not implemented")
}
