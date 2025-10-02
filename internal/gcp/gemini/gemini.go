// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gemini implements access to Google's Gemini model.
//
// [Client] implements [llm.Embedder] and [llm.GenerateContent]. Use [NewClient] to connect.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/secret"
	"google.golang.org/genai"
)

// Scrub is a request scrubber for use with [rsc.io/httprr].
func Scrub(req *http.Request) error {
	req.Header.Del("X-Goog-Api-Key")                  // delete API key
	req.Header.Del("X-Goog-Api-Client")               // contains version numbers
	req.Header.Set("User-Agent", "gemini httprecord") // contains same version numbers

	if ctype := req.Header.Get("Content-Type"); ctype == "application/json" || strings.HasPrefix(ctype, "application/json;") {
		// Canonicalize JSON body.
		// google.golang.org/protobuf/internal/encoding.json
		// goes out of its way to randomize the JSON encodings
		// of protobuf messages by adding or not adding spaces
		// after commas. Derandomize by compacting the JSON.
		b := req.Body.(*httprr.Body)
		var buf bytes.Buffer
		if err := json.Compact(&buf, b.Data); err == nil {
			b.Data = buf.Bytes()
		}
	}
	return nil
}

// A Client represents a connection to Gemini.
type Client struct {
	slog            *slog.Logger
	genai           *genai.Client
	generativeModel string
	embeddingModel  string
	dim             int32
	temperature     float32 // negative means use default
}

const (
	DefaultEmbeddingModel  = "text-embedding-004"
	DefaultGenerativeModel = "gemini-2.5-pro"
)

// forceEmbedDim is the hard-coded forced dimensionality of
// vectors returned by this package. The original Gemini models
// used 768, and so our databases are configured for 768.
// Newer Gemini models produce larger embeddings, up to 3072,
// but truncating to 768 is still plenty good enough, because the
// vectors are arranged so that the most important information is
// in the leading entries. We just need to renormalize after truncation.
// See https://ai.google.dev/gemini-api/docs/embeddings#control-embedding-size
const forceEmbedDim = 768

// NewClient returns a connection to Gemini, using the given logger and HTTP client.
// It expects to find a secret of the form "AIza..." or "user:AIza..." in sdb
// under the name "ai.google.dev".
// The embeddingModel is the model name to use for embedding, such as text-embedding-004,
// and the generativeModel is the model name to use for generation, such as gemini-1.5-pro.
func NewClient(ctx context.Context, lg *slog.Logger, sdb secret.DB, hc *http.Client, embeddingModel, generativeModel string) (*Client, error) {
	key, ok := sdb.Get("ai.google.dev")
	if !ok {
		return nil, fmt.Errorf("missing api key for ai.google.dev")
	}
	// If key is from .netrc, ignore user name.
	if _, pass, ok := strings.Cut(key, ":"); ok {
		key = pass
	}

	ai, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:     key,
		HTTPClient: hc,
	})
	if err != nil {
		return nil, err
	}

	return &Client{
		slog:            lg,
		genai:           ai,
		embeddingModel:  embeddingModel,
		dim:             forceEmbedDim,
		generativeModel: generativeModel,
		temperature:     -1,
	}, nil
}

const maxBatch = 100 // empirical limit

var _ llm.Embedder = (*Client)(nil)

// EmbeddingModel returns the name of the embedding model.
// It is of the form name/dim where dim is the dimensionality.
func (c *Client) EmbeddingModel() string {
	return fmt.Sprintf("%s/%d", c.embeddingModel, c.dim)
}

// EmbedDocs returns the vector embeddings for the docs,
// implementing [llm.Embedder].
func (c *Client) EmbedDocs(ctx context.Context, docs []llm.EmbedDoc) ([]llm.Vector, error) {
	config := &genai.EmbedContentConfig{
		OutputDimensionality: &c.dim,
	}
	var vecs []llm.Vector
	for docs := range slices.Chunk(docs, maxBatch) {
		var contents []*genai.Content
		for _, d := range docs {
			contents = append(contents, genai.Text("Title: "+d.Title+"\n\n"+d.Text)...)
		}
		resp, err := c.genai.Models.EmbedContent(ctx, c.embeddingModel, contents, config)
		if err != nil {
			return nil, err
		}
		for _, e := range resp.Embeddings {
			vecs = append(vecs, llm.Vector(e.Values).Normal())
		}
	}
	return vecs, nil
}

var _ llm.ContentGenerator = (*Client)(nil)

// Model returns the name of the client's generative model.
func (c *Client) Model() string {
	return c.generativeModel
}

// SetTemperature sets the temperature of the client's generative model.
func (c *Client) SetTemperature(t float32) {
	c.temperature = t
}

// GenerateContent returns the model's response for the prompt parts,
// implementing [llm.ContentGenerator.GenerateContent].
func (c *Client) GenerateContent(ctx context.Context, schema *llm.Schema, promptParts []llm.Part) (string, error) {
	// Generate plain text.
	if schema == nil {
		text, err := c.generate(ctx, "text/plain", nil, promptParts...)
		if err != nil {
			return "", fmt.Errorf("gemini.GenerateContent: %w", err)
		}
		return text, nil
	}

	// Generate JSON.
	text, err := c.generate(ctx, "application/json", genaiSchema(schema), promptParts...)
	if err != nil {
		return "", fmt.Errorf("gemini.GenerateContent: %w", err)
	}
	return text, nil
}

// generate returns the model's response (of the specified MIME type) for the prompt parts.
// It returns an error if a response cannot be generated.
func (c *Client) generate(ctx context.Context, mimeType string, schema *genai.Schema, promptParts ...llm.Part) (string, error) {
	parts, err := c.parts(promptParts)
	if err != nil {
		return "", err
	}
	config := &genai.GenerateContentConfig{
		CandidateCount:   1,
		ResponseMIMEType: mimeType,
		ResponseSchema:   schema,
	}
	if c.temperature >= 0 {
		config.Temperature = &c.temperature
	}
	resp, err := c.genai.Models.GenerateContent(ctx, c.generativeModel, []*genai.Content{{Role: genai.RoleUser, Parts: parts}}, config)
	if err != nil {
		return "", err
	}
	text := resp.Text()
	if text == "" {
		return "", errors.New("no content generated")
	}
	return text, nil
}

// parts converts the given prompt parts to [genai.Part]s of
// their corresponding type.
func (c *Client) parts(promptParts []llm.Part) ([]*genai.Part, error) {
	var parts = make([]*genai.Part, len(promptParts))
	for i, p := range promptParts {
		switch p := p.(type) {
		case llm.Text:
			parts[i] = genai.NewPartFromText(string(p))
		case llm.Blob:
			parts[i] = genai.NewPartFromBytes(p.Data, p.MIMEType)
		default:
			return nil, fmt.Errorf("bad type for part: %T; need string or llm.Blob", p)
		}
	}
	return parts, nil
}
