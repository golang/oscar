// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTimeStamp(t *testing.T) {
	testTime := time.Date(2024, time.July, 30, 12, 1, 2, 0, time.UTC)
	ts := TimeStamp(testTime)
	if !ts.Time().Equal(testTime) {
		t.Errorf("ts.Time() = %v, want %v", ts.Time(), testTime)
	}

	encoded, err := json.Marshal(&ts)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `"2024-07-30 12:01:02"`
	if string(encoded) != wantJSON {
		t.Errorf("marshaled as %q, want %q", encoded, wantJSON)
	}

	var decoded TimeStamp
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Time().Equal(testTime) {
		t.Errorf("decoded TimeStamp = %v, want %v", decoded.Time(), testTime)
	}
}
