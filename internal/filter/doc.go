// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package filter implements https://google.aip.dev/160.
// This is used to filter a collection using a "structured
// syntax accessible to a non-technical audience."
//
// A filter expression may use a field name to be found in a struct.
// If there is no exact match of the field name,
// the filter code will look for a camel case match,
// in which underscores will be skipped and characters following
// an underscore will match case-insensitively.
// For example, Field_name will match FieldName.
// The filter code will also look for a json struct field tag,
// and compare against the JSON name if there is one,
// again doing a camel case match.
// This approach makes it easier to use the same filter expression
// across languages when filtering protobuf types.
// If a struct field is a pointer type,
// it will be dereferenced before being used in any filter comparison.
//
// When a filter expression fails to find a field name,
// it will fall back to looking for an exported method with the name,
// again doing a camel case lookup.
// If there is a matching method, and the method takes no arguments
// and returns either a single value or a single value and an error,
// the filter code will call the method to get the value
// to search for. If the method returns an error,
// the value will simply be ignored;
// the error will not be returned or logged.
// If the method returns a pointer result, it will be dereferenced
// before being used in any filter comparison.
package filter
