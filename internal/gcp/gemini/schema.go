// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gemini

import (
	"golang.org/x/oscar/internal/llm"
	"google.golang.org/genai"
)

// genaiSchema trivially converts an [llm.Schema] to a [genai.Schema].
// The types have the exact same underlying structure, and are copied only
// to avoid a direct dependency on google.golang.org/genai
// by other packages.
func genaiSchema(s *llm.Schema) *genai.Schema {
	if s == nil {
		return nil
	}
	var props map[string]*genai.Schema
	if len(s.Properties) != 0 {
		props = make(map[string]*genai.Schema, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = genaiSchema(v)
		}
	}
	return &genai.Schema{
		Type:        genaiType(s.Type),
		Format:      s.Format,
		Description: s.Description,
		Nullable:    &s.Nullable,
		Enum:        s.Enum,
		Items:       genaiSchema(s.Items),
		Properties:  props,
		Required:    s.Required,
	}
}

func genaiType(t llm.Type) genai.Type {
	switch t {
	case llm.TypeUnspecified:
		return genai.TypeUnspecified
	case llm.TypeString:
		return genai.TypeString
	case llm.TypeNumber:
		return genai.TypeNumber
	case llm.TypeInteger:
		return genai.TypeInteger
	case llm.TypeBoolean:
		return genai.TypeBoolean
	case llm.TypeArray:
		return genai.TypeArray
	case llm.TypeObject:
		return genai.TypeObject
	}
	return "???"
}
