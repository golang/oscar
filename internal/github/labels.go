// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"context"
	"encoding/json"
	"net/url"
)

// DownloadLabel downloads information about a label from GitHub.
func (c *Client) DownloadLabel(ctx context.Context, project, name string) (Label, error) {
	var lab Label
	_, err := c.get(ctx, labelURL(project, name), "", &lab)
	if err != nil {
		return Label{}, err
	}
	return lab, nil
}

// CreateLabel creates a new label.
func (c *Client) CreateLabel(ctx context.Context, project string, lab Label) error {
	if c.divertEdits() {
		c.testMu.Lock()
		defer c.testMu.Unlock()

		c.testEdits = append(c.testEdits, &TestingEdit{
			Project: project,
			Label:   lab,
		})
		return nil
	}
	_, err := c.post(ctx, labelURL(project, ""), lab)
	return err
}

// LabelChanges specifies changes to make to a label.
// Only non-empty fields will be changed.
type LabelChanges struct {
	NewName     string `json:"new_name,omitempty"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
}

// EditLabel changes a label.
func (c *Client) EditLabel(ctx context.Context, project, name string, changes LabelChanges) error {
	if c.divertEdits() {
		c.testMu.Lock()
		defer c.testMu.Unlock()

		c.testEdits = append(c.testEdits, &TestingEdit{
			Project:      project,
			Label:        Label{Name: name},
			LabelChanges: &changes,
		})
		return nil
	}
	_, err := c.patch(ctx, labelURL(project, name), changes)
	return err
}

var labelPageQueryParams = url.Values{
	"page":     {"1"},
	"per_page": {"100"},
}

// ListLabels lists all the labels in a project.
func (c *Client) ListLabels(ctx context.Context, project string) ([]Label, error) {
	var labels []Label
	for p, err := range c.pages(ctx, labelURL(project, "")+"?"+labelPageQueryParams.Encode(), "") {
		if err != nil {
			return nil, err
		}
		for _, raw := range p.body {
			var lab Label
			if err := json.Unmarshal(raw, &lab); err != nil {
				return nil, err
			}
			labels = append(labels, lab)
		}
	}
	return labels, nil
}

// deleteLabel deletes a label.
// For testing only.
func (c *Client) deleteLabel(ctx context.Context, project, name string) error {
	if c.divertEdits() {
		panic("deleteLabel not supported in testing mode")
	}

	var x any
	_, err := c.json(ctx, "DELETE", labelURL(project, name), &x)
	return err
}

func labelURL(project, name string) string {
	u := "https://api.github.com/repos/" + project + "/labels"
	if name == "" {
		return u
	}
	return u + "/" + name
}
