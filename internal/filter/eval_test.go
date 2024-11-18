// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// yamlTests holds the contents of a testdata/*_test.yaml file.
type yamlTests struct {
	Tests []yamlTest `yaml:"tests"`
}

// yamlTest holds the contents of a single test.
type yamlTest struct {
	Description string `yaml:"description"`
	Expr        string `yaml:"expr"`
	Error       string `yaml:"error"`
	Matches     []int  `yaml:"matches"`
	Skip        bool   `yaml:"skip"`
}

type filterBasicTestData struct {
	Resources []resource `json:"resources"`
}

type resource struct {
	BoolField           bool      `json:"bool_field"`
	CaseField           string    `json:"case_field"`
	IntField            int64     `json:"int_field"`
	FloatField          float64   `json:"float_field"`
	EnumField           string    `json:"enum_field"`
	StringField         string    `json:"string_field"`
	TimestampField      time.Time `json:"timestamp_field"`
	Compound            compound  `json:"compound"`
	False               bool      `json:"false"`
	True                bool      `json:"true"`
	Undefined           *string   `json:"undefined"`
	Text                string    `json:"text"`
	URL                 string    `json:"url"`
	Members             []member  `json:"members"`
	Logical             string    `json:"logical"`
	None                *string   `json:"none"`
	QuoteDouble         string    `json:"quote_double"`
	QuoteSingle         string    `json:"quote_single"`
	Subject             string    `json:"subject"`
	Words               string    `json:"words"`
	UnicodeField        string    `json:"unicode_field"`
	ExistsScalarInt     int64     `json:"exists_scalar_int"`
	ExistsScalarString  string    `json:"exists_scalar_string"`
	ExistsScalarMessage member    `json:"exists_scalar_message"`
	ExistsArrayInt      []int64   `json:"exists_array_int"`
	ExistsArrayString   []string  `json:"exists_array_string"`
	ExistsArrayMessage  []member  `json:"exists_array_message"`
}

type member struct {
	First string `json:"first"`
	Last  string `json:"last"`
	ID    int64  `json:"id"`
}

type compound struct {
	IntField    int64         `json:"int_field"`
	StringField string        `json:"string_field"`
	Num         numberObjects `json:"num"`
	Str         stringObjects `json:"str"`
}

type numberObjects struct {
	Array      []float64          `json:"array"`
	Dictionary map[string]float64 `json:"dictionary"`
}

type stringObjects struct {
	Array      []string          `json:"array"`
	Dictionary map[string]string `json:"dictionary"`
	Value      string            `json:"value"`
}

func TestEvalBasic(t *testing.T) {
	var data filterBasicTestData
	unmarshalJSON(t, "basic_test.json", &data)

	var tests yamlTests
	unmarshalYAML(t, "basic_test.yaml", &tests)

	runTests(t, tests.Tests, data.Resources)
}

// runTests runs a set of YAML tests on a set of input data.
func runTests[T any](t *testing.T, tests []yamlTest, data []T) {
	var desc string
	var idx int
	for _, test := range tests {
		if test.Description != "" {
			desc = test.Description
			idx = 1
			if test.Expr == "" {
				continue
			}
		}

		if test.Skip {
			idx++
			continue
		}

		t.Run(fmt.Sprintf("%s %d", desc, idx), func(t *testing.T) {
			runOneTest(t, &test, data)
		})

		idx++
	}
}

// runOneTest runs on YAML test.
func runOneTest[T any](t *testing.T, test *yamlTest, data []T) {
	e, err := ParseFilter(test.Expr)
	if err != nil {
		if test.Error != "" {
			t.Logf("parse of %q failed (%v) when error is expected (%s)", test.Expr, err, test.Error)
			return
		}
		t.Fatalf("can't parse %q: %v", test.Expr, err)
	}

	eval, msgs := Evaluator[T](e, nil)

	if len(msgs) > 0 {
		if test.Error != "" {
			t.Logf("evaluation of %q failed when error is expected (%s)", test.Expr, test.Error)
		} else {
			t.Errorf("%d messages reported when building evaluator for %q", len(msgs), test.Expr)
		}

		for _, msg := range msgs {
			t.Log(msg)
		}

		if test.Error != "" {
			return
		}
	}

	var matches []int
	for i, d := range data {
		if eval(d) {
			matches = append(matches, i+1)
		}
	}

	if !slices.Equal(matches, test.Matches) {
		t.Errorf("got matches %v, want %v", matches, test.Matches)
	}
}

// unmarshalJSON reads JSON encoded data from a testdata file into v.
func unmarshalJSON(t *testing.T, filename string, v any) {
	f, err := os.Open(filepath.Join("testdata", filename))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatal(err)
	}
}

// unmarshalYAML reads YAML encoded data from a testdata file into v.
func unmarshalYAML(t *testing.T, filename string, v any) {
	f, err := os.Open(filepath.Join("testdata", filename))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		t.Fatal(err)
	}
}
