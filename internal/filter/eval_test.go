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
	"strconv"
	"strings"
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

// resourceInterface is an interface with methods that match
// the fields of resource.
//
// A couple of methods return an error (that will always be nil),
// most do not.
type resourceInterface interface {
	BoolField() (bool, error)
	CaseField() string
	IntField() int64
	FloatField() float64
	EnumField() string
	StringField() string
	TimestampField() time.Time
	Compound() compound
	False() bool
	True() bool
	Undefined() *string
	Text() string
	URL() string
	Members() []member
	Logical() string
	None() *string
	QuoteDouble() string
	QuoteSingle() string
	Subject() string
	Words() string
	UnicodeField() string
	ExistsScalarInt() int64
	ExistsScalarString() string
	ExistsScalarMessage() member
	ExistsArrayInt() []int64
	ExistsArrayString() []string
	ExistsArrayMessage() ([]member, error)
}

// resourceWrapper is a type that implements resourceInterface
// using a value of type resource.
type resourceWrapper struct {
	r resource
}

func (rw resourceWrapper) BoolField() (bool, error) {
	return rw.r.BoolField, nil
}

func (rw resourceWrapper) CaseField() string {
	return rw.r.CaseField
}

func (rw resourceWrapper) IntField() int64 {
	return rw.r.IntField
}

func (rw resourceWrapper) FloatField() float64 {
	return rw.r.FloatField
}

func (rw resourceWrapper) EnumField() string {
	return rw.r.EnumField
}

func (rw resourceWrapper) StringField() string {
	return rw.r.StringField
}

func (rw resourceWrapper) TimestampField() time.Time {
	return rw.r.TimestampField
}

func (rw resourceWrapper) Compound() compound {
	return rw.r.Compound
}

func (rw resourceWrapper) False() bool {
	return rw.r.False
}

func (rw resourceWrapper) True() bool {
	return rw.r.True
}

func (rw resourceWrapper) Undefined() *string {
	return rw.r.Undefined
}

func (rw resourceWrapper) Text() string {
	return rw.r.Text
}

func (rw resourceWrapper) URL() string {
	return rw.r.URL
}

func (rw resourceWrapper) Members() []member {
	return rw.r.Members
}

func (rw resourceWrapper) Logical() string {
	return rw.r.Logical
}

func (rw resourceWrapper) None() *string {
	return rw.r.None
}

func (rw resourceWrapper) QuoteDouble() string {
	return rw.r.QuoteDouble
}

func (rw resourceWrapper) QuoteSingle() string {
	return rw.r.QuoteSingle
}

func (rw resourceWrapper) Subject() string {
	return rw.r.Subject
}

func (rw resourceWrapper) Words() string {
	return rw.r.Words
}

func (rw resourceWrapper) UnicodeField() string {
	return rw.r.UnicodeField
}

func (rw resourceWrapper) ExistsScalarInt() int64 {
	return rw.r.ExistsScalarInt
}

func (rw resourceWrapper) ExistsScalarString() string {
	return rw.r.ExistsScalarString
}

func (rw resourceWrapper) ExistsScalarMessage() member {
	return rw.r.ExistsScalarMessage
}

func (rw resourceWrapper) ExistsArrayInt() []int64 {
	return rw.r.ExistsArrayInt
}

func (rw resourceWrapper) ExistsArrayString() []string {
	return rw.r.ExistsArrayString
}

func (rw resourceWrapper) ExistsArrayMessage() ([]member, error) {
	return rw.r.ExistsArrayMessage, nil
}

// resourcePointerInterface is like resourceInterface,
// but Members returns []*member. This tests a slice of pointers.
type resourcePointerInterface interface {
	BoolField() (bool, error)
	CaseField() string
	IntField() int64
	FloatField() float64
	EnumField() string
	StringField() string
	TimestampField() time.Time
	Compound() compound
	False() bool
	True() bool
	Undefined() *string
	Text() string
	URL() string
	Members() []*member
	Logical() string
	None() *string
	QuoteDouble() string
	QuoteSingle() string
	Subject() string
	Words() string
	UnicodeField() string
	ExistsScalarInt() int64
	ExistsScalarString() string
	ExistsScalarMessage() member
	ExistsArrayInt() []int64
	ExistsArrayString() []string
	ExistsArrayMessage() ([]member, error)
}

// resourcePointerWrapper is like resourceWrapper,
// but Members returns []*member. This tests a slice of pointers.
type resourcePointerWrapper struct {
	resourceWrapper
}

func (rpw resourcePointerWrapper) Members() []*member {
	members := rpw.resourceWrapper.Members()
	ret := make([]*member, len(members))
	for i := range members {
		ret[i] = &members[i]
	}
	return ret
}

func TestEvalBasic(t *testing.T) {
	var data filterBasicTestData
	unmarshalJSON(t, "basic_test.json", &data)

	var tests yamlTests
	unmarshalYAML(t, "basic_test.yaml", &tests)

	t.Run("struct", func(t *testing.T) {
		runTests(t, tests.Tests, data.Resources)
	})

	t.Run("interface", func(t *testing.T) {
		rws := make([]resourceInterface, len(data.Resources))
		for i, r := range data.Resources {
			rws[i] = resourceWrapper{r}
		}
		runTests(t, tests.Tests, rws)
	})

	t.Run("interface-pointer", func(t *testing.T) {
		rws := make([]resourcePointerInterface, len(data.Resources))
		for i, r := range data.Resources {
			rws[i] = resourcePointerWrapper{resourceWrapper{r}}
		}
		runTests(t, tests.Tests, rws)
	})
}

type filterMiscTestData struct {
	Resources []testMessage `json:"resources"`
}

type testMessage struct {
	Int32Field                 int32                    `json:"int32_field"`
	Int64Field                 int64                    `json:"int64_field"`
	Uint32Field                uint32                   `json:"uint32_field"`
	Uint64Field                uint64                   `json:"uint64_field"`
	FloatField                 float32                  `json:"float_field"`
	FloatInfinity              float32Special           `json:"float_infinity"`
	FloatNegativeInfinity      float32Special           `json:"float_negative_infinity"`
	FloatNaN                   float32Special           `json:"float_nan"`
	DoubleField                float64Special           `json:"double_field"`
	DoubleInfinity             float64Special           `json:"double_infinity"`
	DoubleNegativeInfinity     float64Special           `json:"double_negative_infinity"`
	DoubleNaN                  float64Special           `json:"double_nan"`
	BoolField                  bool                     `json:"bool_field"`
	StringField                string                   `json:"string_field"`
	EnumField                  string                   `json:"enum_field"`
	OutOfOrderEnumField        string                   `json:"out_of_order_enum_field"`
	BytesField                 string                   `json:"bytes_field"`
	NoValueField               string                   `json:"no_value_field"`
	Nested                     nestedMessage            `json:"nested"`
	DeprecatedField            int64                    `json:"deprecated_field"`
	InternalField              string                   `json:"internal_field"`
	RepeatedInt32Field         []int32                  `json:"repeated_int32_field"`
	RepeatedStringField        []string                 `json:"repeated_string_field"`
	NonUTF8StringField         string                   `json:"non_utf8_string_field"`
	NonUTF8RepeatedStringField []string                 `json:"non_utf8_repeated_string_field"`
	RepeatedEnumField          []string                 `json:"repeated_enum_field"`
	RepeatedMessageField       []nestedMessage          `json:"repeated_message_field"`
	RepeatedEmptyMessageField  []nestedMessage          `json:"repeated_empty_message_field"`
	MapStringIn32Field         map[string]int32         `json:"map_string_int32_field"`
	MapInt32Int32Field         map[int32]int32          `json:"map_int32_int32_field"`
	MapStringNestedField       map[string]nestedMessage `json:"map_string_nested_field"`
	AnyField                   any                      `json:"any_field"`
	RepeatedAnyField           []any                    `json:"repeated_any_field"`
	Timestamp                  time.Time                `json:"timestamp"`
	Duration                   duration                 `json:"duration"`
	StructField                map[string]any           `json:"struct_field"`
	JSON                       map[string]any           `json:"json"`
	StringValue                string                   `json:"string_value"`
	NestedValue                nestedMessage            `json:"nested_value"`
	UnicodePathe               string                   `json:"unicode_pathe"`
	UnicodeResume              string                   `json:"unicode_resume"`
	UnicodeUnicode             string                   `json:"unicode_unicode"`
	URLField                   string                   `json:"url_field"`
}

type nestedMessage struct {
	Uint32Field         uint32              `json:"uint32_field"`
	DeeperNest          deeperNestedMessage `json:"deeper_nest"`
	RepeatedUint32Field []uint32            `json:"repeated_uint32_field"`
	RepeatedEmptyUint32 []uint32            `json:"repeated_empty_uint32"`
	StringField         string              `json:"string_field"`
	NovalueField        string              `json:"no_value_field"`
}

type deeperNestedMessage struct {
	NiceField  string `json:"nice_field"`
	NicerField string `json:"nicer_field"`
}

// float32Special is a version of float32 that supports JSON
// unmarshaling of Infinity and NaN.
type float32Special float32

func (pf *float32Special) UnmarshalJSON(text []byte) error {
	str := strings.Trim(string(text), `"`)
	f, err := strconv.ParseFloat(str, 32)
	if err != nil {
		return err
	}
	*pf = float32Special(f)
	return nil
}

// float64Special is a version of float64 that supports JSON
// unmarshaling of Infinity and NaN.
type float64Special float64

func (pf *float64Special) UnmarshalJSON(text []byte) error {
	str := strings.Trim(string(text), `"`)
	f, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return err
	}
	*pf = float64Special(f)
	return nil
}

// duration is a version of time.Duration that supports JSON unmarshaling.
type duration time.Duration

func (pd *duration) UnmarshalJSON(text []byte) error {
	str := strings.Trim(string(text), `"`)
	d, err := time.ParseDuration(str)
	if err != nil {
		return err
	}
	*pd = duration(d)
	return nil
}

func TestEvalMisc(t *testing.T) {
	var data filterMiscTestData
	unmarshalJSON(t, "misc_test.json", &data)

	var tests yamlTests
	unmarshalYAML(t, "misc_test.yaml", &tests)

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
			// Check whether the test passes.
			e, err := ParseFilter(test.Expr)
			if err == nil {
				eval, msgs := Evaluator[T](e, nil)
				if len(msgs) == 0 && test.Error == "" {
					var matches []int
					for i, d := range data {
						if eval(d) {
							matches = append(matches, i+1)
						}
					}
					if slices.Equal(matches, test.Matches) {
						t.Errorf("%s %d passes unexpectedly", desc, idx)
					}
				}
			}

			idx++
			continue
		}

		t.Run(fmt.Sprintf("%s %d", desc, idx), func(t *testing.T) {
			runOneTest(t, &test, data)
		})

		idx++
	}
}

// runOneTest runs one YAML test.
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

// Test that we don't panic on an incomparable literal.
func TestIncomparable(t *testing.T) {
	e1, err := ParseFilter("A:0")
	if err != nil {
		t.Fatal(err)
	}
	e2, err := ParseFilter("0")
	if err != nil {
		t.Fatal(err)
	}

	type Incomparable1 struct {
		A [][]byte
	}

	// Building the evaluator should fail,
	// because we can't compare 0 to []byte.
	_, msgs := Evaluator[Incomparable1](e1, nil)
	if len(msgs) == 0 {
		t.Error("expected evaluator errors")
	} else {
		for _, msg := range msgs {
			t.Log(msg)
		}
	}

	eval, msgs := Evaluator[Incomparable1](e2, nil)
	if len(msgs) != 0 {
		t.Error("evaluator failed")
		for _, msg := range msgs {
			t.Log(msg)
		}
	} else {
		// Running this should not panic.
		if eval(Incomparable1{A: [][]byte{[]byte{1}}}) {
			t.Error("unexpected match")
		}
	}

	type Incomparable2 struct {
		A []any
	}

	for i, expr := range []Expr{e1, e2} {
		// This case should succeed, because we can compare 0 to any.
		eval, msgs := Evaluator[Incomparable2](expr, nil)
		if len(msgs) != 0 {
			t.Errorf("%d: evaluator failed", i)
			for _, msg := range msgs {
				t.Logf("%d: %s", i, msg)
			}
		} else {
			// Running this with an incomparable type in a
			// should not panic.
			if eval(Incomparable2{A: []any{[]byte{0}}}) {
				t.Errorf("%d: unexpected match", i)
			}
		}
	}
}
