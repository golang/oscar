// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/tools/txtar"
)

// divertChanges reports whether changes and their
// comments are being diverted for testing purposes.
func (c *Client) divertChanges() bool {
	return c.testing && c.testClient != nil
}

// Testing returns a TestingClient, which provides access to Client functionality
// intended for testing.
// Testing only returns a non-nil TestingClient in testing mode,
// which is active if the current program is a test binary (that is, [testing.Testing] returns true).
// Otherwise, Testing returns nil.
//
// Each Client has only one TestingClient associated with it. Every call to Testing returns the same TestingClient.
func (c *Client) Testing() *TestingClient {
	if !testing.Testing() && !c.testing {
		return nil
	}

	if c.testClient == nil {
		c.testClient = &TestingClient{}
	}
	return c.testClient
}

// A TestingClient provides access to [Client] functionality intended for testing.
type TestingClient struct {
	chs        []*ChangeInfo // change updates, in reverse chronological order
	queryLimit int           // mimic Gerrit query limits
}

// LoadTxtar loads a change info history from the named txtar file,
// and adds it to tc.chs.
//
// The file should contain a txtar archive (see [golang.org/x/tools/txtar]).
// Each file in the archive may be named “change#n” (for example “change#1”).
// A line in the file must be in the format "key: value", where "key" is one
// of the fields of [ChangeInfo] type.
func (tc *TestingClient) LoadTxtar(file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	err = tc.LoadTxtarData(data)
	if err != nil {
		err = &os.PathError{Op: "load", Path: file, Err: err}
	}
	return err
}

// timeStampType is the [reflect.Type] of [TimeStamp].
var timeStampType = reflect.TypeFor[TimeStamp]()

// accountInfoType is the [reflect.Type] of [*AccountInfo].
var accountInfoType = reflect.TypeFor[*AccountInfo]()

// LoadTxtarData loads a change info history from the txtar file content data.
// See [LoadTxtar] for a description of the format.
func (tc *TestingClient) LoadTxtarData(data []byte) error {
	ar := txtar.Parse(data)
	for _, file := range ar.Files {
		data := string(file.Data)
		// Skip the name and proceed to read headers.
		c := &ChangeInfo{}
		cv := reflect.ValueOf(c).Elem()
		if _, err := tc.setFields(file.Name, data, 0, cv); err != nil {
			return err
		}
		tc.chs = append(tc.chs, c)
	}
	return nil
}

// setFields reads field values and sets fields in a struct accordingly.
// The indent parameter says how many spaces are required on each line;
// this supports fields that are themselves structs.
// This returns the remaining data.
func (tc *TestingClient) setFields(filename, data string, indent int, st reflect.Value) (string, error) {
	prefix := strings.Repeat(" ", indent)
	for {
		line, rest, _ := strings.Cut(data, "\n")
		if line == "" {
			data = rest
			break
		}
		line, ok := strings.CutPrefix(line, prefix)
		if !ok {
			// Don't change data.
			break
		}
		data = rest
		rest, err := tc.setField(filename, line, data, indent, st)
		if err != nil {
			return "", err
		}
		data = rest
	}
	return data, nil
}

// setField takes a struct and a line that sets a scalar field.
// The line should have the form "key: value",
// where "key" is the name of a field in the struct and
// "value is the value we want to set it to.
// This isn't general, it only handles the cases that arise
// for Gerrit types.
// The data argument is the data following the line,
// used for multi-line values.
// This returns the remaining data.
func (tc *TestingClient) setField(filename string, line, data string, indent int, st reflect.Value) (string, error) {
	key, val, ok := strings.Cut(line, ":")
	if !ok {
		return "", fmt.Errorf("%s: invalid line: %q", filename, line)
	}
	val = strings.TrimSpace(val)

	field := st.FieldByName(key)
	if !field.IsValid() {
		return "", fmt.Errorf("%s: unrecognized field name %q in %s", filename, key, st.Type())
	}

	var vval reflect.Value
	switch field.Type().Kind() {
	case reflect.Bool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return "", fmt.Errorf("%s: field %q: can't parse %q as bool", filename, key, val)
		}
		vval = reflect.ValueOf(b)

	case reflect.Int:
		i, err := strconv.Atoi(val)
		if err != nil {
			return "", fmt.Errorf("%s: field %q: can't parse %q as int", filename, key, val)
		}
		vval = reflect.ValueOf(i)

	case reflect.String:
		vval = reflect.ValueOf(val)

	case reflect.Struct:
		if field.Type() == timeStampType {
			t, err := timestamp(val)
			if err != nil {
				return "", fmt.Errorf("%s: field %q: can't parse %q as timestamp", filename, key, val)
			}
			vval = reflect.ValueOf(TimeStamp(t))
			break
		}
		return "", fmt.Errorf("%s: field %q: unexpected struct type %s", filename, key, field.Type())

	case reflect.Pointer:
		if field.Type() == accountInfoType {
			// For an account we just an email address.
			if val == "" {
				return "", fmt.Errorf("%s: field %q: missing email address for AccountInfo", filename, key)
			}
			vval = reflect.ValueOf(&AccountInfo{Email: val})
			break
		}
		if field.Type().Elem().Kind() != reflect.Struct {
			return "", fmt.Errorf("%s: field %q: unexpected pointer to %s", filename, key, field.Type().Elem())
		}

		// For struct types in general we expect the fields
		// to follow, indented.
		vval = reflect.New(field.Type().Elem())
		rest, err := tc.setFields(filename, data, indent+1, vval.Elem())
		if err != nil {
			return "", err
		}
		data = rest
		break

	case reflect.Slice:
		switch field.Type().Elem().Kind() {
		case reflect.Int:
			// For ints just put all the values on one line.
			var ints []int
			for _, vi := range strings.Fields(val) {
				i, err := strconv.Atoi(vi)
				if err != nil {
					return "", fmt.Errorf("%s: field %q: %v", filename, key, err)
				}
				ints = append(ints, i)
			}
			vval = reflect.ValueOf(ints)

		case reflect.String:
			// For strings just put all the values on one line.
			// Strings are space separated, no quoting.
			var strs []string
			for _, vs := range strings.Fields(val) {
				vs = strings.TrimSpace(vs)
				if vs == "" {
					continue
				}
				strs = append(strs, vs)
			}
			vval = reflect.ValueOf(strs)

		case reflect.Pointer:
			if field.Type().Elem().Elem().Kind() != reflect.Struct {
				return "", fmt.Errorf("%s: field %q: pointer not to struct in type %s", filename, key, field.Type())
			}
			vval = reflect.New(field.Type().Elem().Elem())
			rest, err := tc.setFields(filename, data, indent+1, vval.Elem())
			if err != nil {
				return "", err
			}
			data = rest
			vval = reflect.Append(field, vval)

		default:
			return "", fmt.Errorf("%s: field %q: unsupported slice type %s", filename, key, field.Type())
		}

	case reflect.Map:
		if field.Type().Key().Kind() != reflect.String {
			return "", fmt.Errorf("%s: field %q: unsupported map key type in %s", filename, key, field.Type())
		}

		if field.IsZero() {
			field.Set(reflect.MakeMap(field.Type()))
		}

		switch field.Type().Elem().Kind() {
		case reflect.String:
			mkey, mval, ok := strings.Cut(val, ":")
			if !ok {
				return "", fmt.Errorf("%s: field %q: expected key: val in map to string", filename, key)
			}
			mkey = strings.TrimSpace(mkey)
			mval = strings.TrimSpace(mval)
			field.SetMapIndex(reflect.ValueOf(mkey), reflect.ValueOf(mval))
			// Don't fall through to bottom of function.
			return data, nil

		case reflect.Pointer:
			if field.Type().Elem().Elem().Kind() != reflect.Struct {
				return "", fmt.Errorf("%s: field %q: pointer not to struct in map element type in %s", filename, key, field.Type())
			}
			vval = reflect.New(field.Type().Elem().Elem())
			rest, err := tc.setFields(filename, data, indent+1, vval.Elem())
			if err != nil {
				return "", err
			}
			data = rest
			field.SetMapIndex(reflect.ValueOf(val), vval)
			// Don't fall through to bottom of function.
			return data, nil

		default:
			return "", fmt.Errorf("%s: field %q: unsupported map element type in %s", filename, key, field.Type())
		}

	default:
		return "", fmt.Errorf("%s: field %q: unsupported type %s", filename, key, field.Type())
	}

	if key == "interrupt" {
		// Special case for the only unexported field.
		st.Addr().Interface().(*ChangeInfo).interrupt = vval.Bool()
	} else {
		field.Set(vval)
	}

	return data, nil
}

// changes returns an iterator of change updates in tc.chs that are updated
// in the interval [after, before], in reverse chronological order. First
// skip number of matching change updates are disregarded.
func (tc *TestingClient) changes(_ context.Context, project string, after, before string, skip int) iter.Seq2[json.RawMessage, error] {
	return func(yield func(json.RawMessage, error) bool) {
		skipped := 0
		inInterval := false
		yielded := 0 // yielded in a single batch
		for _, c := range tc.chs {
			in, err := updatedIn(c, after, before)
			if err != nil {
				yield(nil, err)
				return
			}
			if !in {
				if inInterval { // reached outside of the interval
					return
				}
				continue
			}

			// We are inside the matching interval.
			inInterval = true
			if skip > 0 && skipped < skip {
				skipped++
				continue
			}

			cj, err := json.Marshal(c)
			if err != nil {
				yield(nil, err)
				return
			}

			if c.Project != project {
				continue
			}

			yielded++
			if !yield(cj, nil) {
				return
			}

			if c.interrupt { // fake an interruption
				yield(nil, errors.New("test interrupt error"))
				return
			}

			if yielded >= tc.queryLimit { // reached the batch limit
				return
			}
		}
	}
}

// updatedIn reports if c was updated in the [after, before] interval.
// Both after and before must be in gerrit timestamp layout.
func updatedIn(c *ChangeInfo, after, before string) (bool, error) {
	u := c.Updated.Time()

	ain := true
	if after != "" {
		a, err := timestamp(after)
		if err != nil {
			return false, err
		}
		ain = a.Time().Equal(u) || a.Time().Before(u)
	}
	bin := true
	if before != "" {
		b, err := timestamp(before)
		if err != nil {
			return false, err
		}
		bin = b.Time().Equal(u) || b.Time().After(u)
	}
	return ain && bin, nil
}

// change returns the data for a single testing change.
func (tc *TestingClient) change(changeNum int) *Change {
	for _, ch := range tc.chs {
		if ch.Number == changeNum {
			return &Change{
				num:  changeNum,
				data: storage.JSON(ch),
			}
		}
	}
	return nil
}

func timestamp(gt string) (TimeStamp, error) {
	var ts TimeStamp
	if err := ts.UnmarshalJSON([]byte(quote(gt))); err != nil {
		return TimeStamp(time.Time{}), err
	}
	return ts, nil
}

// syncComments does nothing, for testing purposes.
func (tc *TestingClient) syncComments(_ context.Context, _ storage.Batch, _ string, _ int) error {
	return nil
}
