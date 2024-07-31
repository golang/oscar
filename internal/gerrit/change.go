// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// A Change holds information for a single Gerrit change.
// This internally holds JSON encoded data,
// and only decodes what is needed.
type Change struct {
	num  int
	data []byte
}

// ChangeInfo returns a [ChangeInfo] holding almost all the information
// about a [Change]. This does not include the file comments,
// which can be retrieved using the [Change.Comments] method.
func (ch *Change) ChangeInfo() (*ChangeInfo, error) {
	var ci ChangeInfo
	if err := json.Unmarshal(ch.data, &ci); err != nil {
		return nil, fmt.Errorf("can't decode change %d: %v", ch.num, err)
	}
	return &ci, nil
}

// ChangeTimes holds relevant times for a [Change].
type ChangeTimes struct {
	Created   time.Time // when change was created
	Updated   time.Time // when change was updated, zero if never
	Submitted time.Time // when change was submitted, zero if not
	Abandoned time.Time // when change was abandoned, zero if not
}

// Times returns the created, updated, submitted, and abandoned times
// for a change. If the change is not submitted or not abandoned,
// those times will be zero.
func (ch *Change) Times() (ChangeTimes, error) {
	var times struct {
		Created   TimeStamp `json:"created"`
		Updated   TimeStamp `json:"updated"`
		Submitted TimeStamp `json:"submitted"`
		Status    string    `json:"status"`
	}
	if err := json.Unmarshal(ch.data, &times); err != nil {
		err = fmt.Errorf("can't decode change %d: %v", ch.num, err)
		return ChangeTimes{}, err
	}

	created := times.Created.Time()
	updated := times.Updated.Time()
	submitted := times.Submitted.Time()

	var abandoned time.Time
	if times.Status == "ABANDONED" {
		type message struct {
			Date    TimeStamp `json:"date"`
			Message string    `json:"message"`
		}
		var messages struct {
			Messages []message `json:"messages"`
		}
		if err := json.Unmarshal(ch.data, &messages); err != nil {
			err = fmt.Errorf("can't decode change %d: %v", ch.num, err)
			return ChangeTimes{}, err
		}
		for _, msg := range slices.Backward(messages.Messages) {
			if strings.HasPrefix(msg.Message, "Abandoned") {
				abandoned = msg.Date.Time()
				break
			}
		}
		if abandoned.IsZero() {
			err := fmt.Errorf("change %d is abandoned but can't find out where", ch.num)
			return ChangeTimes{}, err
		}
	}

	ret := ChangeTimes{
		Created:   created,
		Updated:   updated,
		Submitted: submitted,
		Abandoned: abandoned,
	}
	return ret, nil
}

// Messages returns the messages on a [Change].
// These are the top-level messages created by clicking on
// the top REPLY button when reviewing a change.
// Inline file comments are returned by [Client.Comments].
func (ch *Change) Messages() ([]ChangeMessageInfo, error) {
	var messages struct {
		Messages []ChangeMessageInfo `json:"messages"`
	}
	if err := json.Unmarshal(ch.data, &messages); err != nil {
		return nil, fmt.Errorf("can't decode change %d: %v", ch.num, err)
	}
	return messages.Messages, nil
}
