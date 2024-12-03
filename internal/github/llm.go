// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"golang.org/x/oscar/internal/llmapp"
)

// ToLLMDoc converts an Issue to a format that can be used as
// an input to an LLM.
func (i *Issue) ToLLMDoc() *llmapp.Doc {
	return &llmapp.Doc{
		Type:   "issue",
		URL:    i.HTMLURL,
		Author: i.User.Login,
		Title:  i.Title,
		Text:   i.Body,
	}
}

// ToLLMDoc converts an IssueComment to a format that can be used as
// an input to an LLM.
func (ic *IssueComment) ToLLMDoc() *llmapp.Doc {
	return &llmapp.Doc{
		Type:   "issue comment",
		URL:    ic.HTMLURL,
		Author: ic.User.Login,
		// no title
		Text: ic.Body,
	}
}
