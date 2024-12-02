// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gemini

import (
	"github.com/google/generative-ai-go/genai"
	"golang.org/x/oscar/internal/llm"
)

// toGenAISchema trivially converts an [llm.Schema] to a [genai.Schema].
// The types have the exact same underlying structure, and are copied only
// to avoid a direct dependency on github.com/google/generative-ai-go/genai
// by other packages.
func toGenAISchema(s *llm.Schema) *genai.Schema {
	if s == nil {
		return nil
	}
	var props map[string]*genai.Schema
	if len(s.Properties) != 0 {
		props = make(map[string]*genai.Schema, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = toGenAISchema(v)
		}
	}
	return &genai.Schema{
		Type:        (genai.Type)(s.Type),
		Format:      s.Format,
		Description: s.Description,
		Nullable:    s.Nullable,
		Enum:        s.Enum,
		Items:       toGenAISchema(s.Items),
		Properties:  props,
		Required:    s.Required,
	}
}
