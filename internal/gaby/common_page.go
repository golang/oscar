// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"

	"github.com/google/safehtml"
	"github.com/google/safehtml/template"
)

// Exec executes the given template on the page.
func Exec(tmpl *template.Template, p page) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// page is a Gaby webpage containing a [CommonPage].
// Any struct that embeds a [CommonPage] implements this interface.
type page interface {
	// do not directly define [isCommonPage].
	isCommonPage()
}

// A CommonPage is a partial representation of a Gaby web page,
// used to store data that is common to many pages.
// The templates in tmpl/common.tmpl are defined on this type.
type CommonPage struct {
	// The ID of the page.
	ID pageID
	// A plain text description of the webpage.
	Description string
	// A list of additional stylesheets to use for this webpage.
	// "/static/style.css" and [pageID.CSS] are always included
	// without needing to be listed here.
	Styles []safeURL
	// The input form.
	Form Form
}

// Implements [page.isCommonPage].
func (*CommonPage) isCommonPage() {}

// A Form is a representation of an HTML form.
type Form struct {
	// (Optional) Description and/or general tips for filling out the form.
	Description string
	// The text to display on the form's submit button.
	SubmitText string
	// The form's inputs.
	Inputs []FormInput
}

// A FormInput represents an input (or a group of inputs)
// to an HTML form.
type FormInput struct {
	Label       string // display text
	Type        string // type to display to the user (for tips section)
	Description string // description of the input and its usage (for tips section)

	Name     safeID // HTML "name"
	Required bool   // whether the input is required

	// Additional data, based on the type of form input.
	Typed typedInput
}

type typedInput interface {
	InputType() string
}

// TextInput is a an HTML "text" input.
type TextInput struct {
	ID    safeID // HTML "id"
	Value string // HTML "value"
}

// Implments [typedInput.InputType].
func (TextInput) InputType() string {
	return "text"
}

// RadioInput is a collection of HTML "radio" inputs.
type RadioInput struct {
	Choices []RadioChoice
}

// Implements [typedInput.InputType].
func (RadioInput) InputType() string {
	return "radio"
}

// RadioChoice is a single HTML "radio" input.
type RadioChoice struct {
	Label   string // display text
	ID      safeID // HTML "id"
	Value   string // HTML "value"
	Checked bool   // whether the button should be checked
}

type pageID string

func (p pageID) Endpoint() string {
	return "/" + string(p)
}

func (p pageID) Title() string {
	if t, ok := titles[p]; ok {
		return t
	}
	return string(p)
}

func (p pageID) CSS() safeURL {
	const cssFmt = "/static/%{p}.css"
	u, err := safehtml.TrustedResourceURLFormatFromConstant(cssFmt, map[string]string{"p": string(p)})
	if err != nil {
		panic(err)
	}
	return u
}

// Shorthands for safehtml types.
type (
	safeID  = safehtml.Identifier
	safeURL = safehtml.TrustedResourceURL
)

// Shorthands for safehtml functions.
var (
	toSafeID  = safehtml.IdentifierFromConstant
	toSafeURL = safehtml.TrustedResourceURLFromConstant
)
