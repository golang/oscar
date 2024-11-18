// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"
)

var update = flag.Bool("update", false, "update test output")

func init() {
	flag.BoolVar(&parseTrace, "trace-parse", false, "trace parser")
}

func TestParse(t *testing.T) {
	tests, err := filepath.Glob("testdata/parse/*.txt")
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range tests {
		t.Run(strings.TrimPrefix(strings.TrimSuffix(test, ".txt"), "testdata/parse/"), func(t *testing.T) {
			testParseFile(t, test)
		})
	}
}

const (
	testOutSuff = ".out"
	testErrSuff = ".err"
)

func testParseFile(t *testing.T, test string) {
	ar, err := txtar.ParseFile(test)
	if err != nil {
		t.Fatal(err)
	}
	i := 0
	var newFiles []txtar.File
	for i < len(ar.Files) {
		name := ar.Files[i].Name

		base, ok := strings.CutSuffix(name, ".test")
		if !ok {
			t.Fatalf("archive format error: found %s when expecting a test", name)
		}

		t.Run(base, func(t *testing.T) {
			newFiles = testParseTest(t, ar, i, newFiles)
		})

		i++
		if i < len(ar.Files) {
			nname := ar.Files[i].Name
			if nname == base+testOutSuff || nname == base+testErrSuff {
				i++
			}
		}
	}

	if *update {
		ar.Files = newFiles
		if err := os.WriteFile(test, txtar.Format(ar), 0o666); err != nil {
			t.Errorf("error writing out %s: %v", test, err)
		}
	}
}

func testParseTest(t *testing.T, ar *txtar.Archive, i int, newFiles []txtar.File) []txtar.File {
	base := strings.TrimSuffix(ar.Files[i].Name, ".test")

	expr, err := ParseFilter(string(ar.Files[i].Data))

	if *update {
		newFiles = append(newFiles, ar.Files[i])
		i++

		if i < len(ar.Files) {
			nextName := ar.Files[i].Name
			if strings.HasSuffix(nextName, testOutSuff) || strings.HasSuffix(nextName, testErrSuff) {
				i++
			}
		}

		if err != nil {
			newFiles = append(newFiles, txtar.File{
				Name: base + testErrSuff,
				Data: []byte(err.Error()),
			})
		} else {
			str := ""
			if expr != nil {
				str = expr.String()
			}
			newFiles = append(newFiles, txtar.File{
				Name: base + testOutSuff,
				Data: []byte(str),
			})
		}

		return newFiles
	}

	i++

	if i >= len(ar.Files) {
		t.Fatal("missing result")
	}

	want := string(ar.Files[i].Data)

	if ar.Files[i].Name == base+testOutSuff {
		if err != nil {
			t.Errorf("got error %v, expected no error", err)
		} else {
			got := ""
			if expr != nil {
				got = expr.String()
			}
			if got != want {
				t.Errorf("got %s want %s", got, want)
			}
		}
	} else if ar.Files[i].Name == base+testErrSuff {
		if err == nil {
			t.Errorf("got no error, expected error %s", want)
		} else {
			got := err.Error()
			if got != strings.TrimSpace(want) {
				t.Errorf("got error %s, want %s", got, want)
			}
		}
	} else {
		t.Fatalf("unexpected name %s does not end in %s or %s", ar.Files[i].Name, testOutSuff, testErrSuff)
	}

	return nil
}
