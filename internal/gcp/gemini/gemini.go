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
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/secret"
	"google.golang.org/api/option"
)

// Scrub is a request scrubber for use with [rsc.io/httprr].
func Scrub(req *http.Request) error {
	delete(req.Header, "x-goog-api-key")    // genai does not canonicalize
	req.Header.Del("X-Goog-Api-Key")        // in case it starts
	delete(req.Header, "x-goog-api-client") // contains version numbers
	req.Header.Del("X-Goog-Api-Client")

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
	slog                            *slog.Logger
	genai                           *genai.Client
	embeddingModel, generativeModel string
	temperature                     float32 // negative means use default
}

const (
	DefaultEmbeddingModel  = "text-embedding-004"
	DefaultGenerativeModel = "gemini-1.5-pro"
)

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

	// Ideally this would use use “option.WithAPIKey(key), option.WithHTTPClient(hc),”
	// but using option.WithHTTPClient bypasses the code that passes along the API key.
	// Instead we make our own derived http.Client that re-adds the key.
	// And then we still have to say option.WithAPIKey("ignored") because
	// otherwise NewClient complains that we haven't passed in a key.
	// (If we pass in the key, it ignores it, but if we don't pass it in,
	// it complains that we didn't give it a key.)
	ai, err := genai.NewClient(ctx,
		option.WithAPIKey("ignored"),
		option.WithHTTPClient(withKey(hc, key)))
	if err != nil {
		return nil, err
	}

	return &Client{
		slog:            lg,
		genai:           ai,
		embeddingModel:  embeddingModel,
		generativeModel: generativeModel,
		temperature:     -1,
	}, nil
}

// withKey returns a new http.Client that is the same as hc
// except that it adds "x-goog-api-key: key" to every request.
func withKey(hc *http.Client, key string) *http.Client {
	c := *hc
	t := c.Transport
	if t == nil {
		t = http.DefaultTransport
	}
	c.Transport = &transportWithKey{t, key}
	return &c
}

// transportWithKey is the same as rt
// except that it adds "x-goog-api-key: key" to every request.
type transportWithKey struct {
	rt  http.RoundTripper
	key string
}

func (t *transportWithKey) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	r := *req
	r.Header = maps.Clone(req.Header)
	r.Header["x-goog-api-key"] = []string{t.key}
	return t.rt.RoundTrip(&r)
}

const maxBatch = 100 // empirical limit

var _ llm.Embedder = (*Client)(nil)

// EmbedDocs returns the vector embeddings for the docs,
// implementing [llm.Embedder].
func (c *Client) EmbedDocs(ctx context.Context, docs []llm.EmbedDoc) ([]llm.Vector, error) {
	model := c.genai.EmbeddingModel(c.embeddingModel)
	var vecs []llm.Vector
	for docs := range slices.Chunk(docs, maxBatch) {
		b := model.NewBatch()
		for _, d := range docs {
			b.AddContentWithTitle(d.Title, genai.Text(d.Text))
		}
		resp, err := model.BatchEmbedContents(ctx, b)
		if err != nil {
			return vecs, err
		}
		for _, e := range resp.Embeddings {
			vecs = append(vecs, e.Values)
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
		texts, err := c.generate(ctx, "text/plain", nil, promptParts...)
		if err != nil {
			return "", fmt.Errorf("gemini.GenerateContent: %w", err)
		}
		return strings.Join(texts, "\n"), nil
	}

	// Generate JSON.
	texts, err := c.generate(ctx, "application/json", toGenAISchema(schema), promptParts...)
	if err != nil {
		return "", fmt.Errorf("gemini.GenerateContent: %w", err)
	}
	// Return just the first response, as it's not clear how to concatenate
	// multiple JSON responses.
	return texts[0], nil
}

// generate returns the model's response (of the specified MIME type) for the prompt parts.
// It returns an error if a response cannot be generated.
func (c *Client) generate(ctx context.Context, mimeType string, schema *genai.Schema, promptParts ...llm.Part) ([]string, error) {
	parts, err := c.parts(promptParts)
	if err != nil {
		return nil, err
	}
	resp, err := c.model(mimeType, schema).GenerateContent(ctx, parts...)
	if err != nil {
		return nil, err
	}
	if texts := responses(resp); len(texts) > 0 {
		return texts, nil
	}
	return nil, errors.New("no content generated")
}

// model returns a new instance of the generative model
// for this client, with the candidate count set to 1,
// the MIME type set to mimeType, and the response schema set
// to schema.
func (c *Client) model(mimeType string, schema *genai.Schema) *genai.GenerativeModel {
	model := c.genai.GenerativeModel(c.generativeModel)
	model.SetCandidateCount(1)
	model.ResponseMIMEType = mimeType
	model.ResponseSchema = schema
	if c.temperature >= 0 {
		model.SetTemperature(c.temperature)
	}
	return model
}

// parts converts the given prompt parts to [genai.Part]s of
// their corresponding type.
func (c *Client) parts(promptParts []llm.Part) ([]genai.Part, error) {
	var parts = make([]genai.Part, len(promptParts))
	for i, p := range promptParts {
		switch p := p.(type) {
		case llm.Text:
			parts[i] = genai.Text(p)
		case llm.Blob:
			parts[i] = genai.Blob{
				MIMEType: p.MIMEType,
				Data:     p.Data,
			}
		default:
			return nil, fmt.Errorf("bad type for part: %T; need string or llm.Blob", p)
		}
	}
	return parts, nil
}

// responses parses all text responses from the response.
func responses(resp *genai.GenerateContentResponse) (rs []string) {
	for _, c := range resp.Candidates {
		if c.Content != nil {
			for _, p := range c.Content.Parts {
				if txt, ok := p.(genai.Text); ok {
					rs = append(rs, string(txt))
				}
			}
		}
	}
	return rs
}
