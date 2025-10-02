// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package embeddocs implements embedding text docs into a vector database.
package embeddocs

import (
	"context"
	"fmt"
	"log/slog"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
)

// Sync reads new documents from dc, embeds them using embed,
// and then writes the (docid, vector) pairs to vdb.
//
// Sync uses [docs.DocWatcher] with the given watcher name to
// save its position across multiple calls.
//
// Sync logs status and unexpected problems to lg.
func Sync(ctx context.Context, lg *slog.Logger, vdb storage.VectorDB, embed llm.Embedder, dc *docs.Corpus) error {
	model := embed.EmbeddingModel()
	lg.Info("embeddocs sync", "model", model)

	const batchSize = 100
	var (
		batch     []llm.EmbedDoc
		ids       []string
		batchLast timed.DBTime
	)
	w := dc.DocWatcher(watcherKey(model))

	flush := func() error {
		vecs, err := embed.EmbedDocs(ctx, batch)
		if len(vecs) > len(ids) {
			return fmt.Errorf("embeddocs %s length mismatch: batch=%d vecs=%d ids=%d", model, len(batch), len(vecs), len(ids))
		}
		vbatch := vdb.Batch()
		for i, v := range vecs {
			vbatch.Set(ids[i], v)
		}
		vbatch.Apply()
		if err != nil {
			return fmt.Errorf("embeddocs %s EmbedDocs error: %w", model, err)
		}
		if len(vecs) != len(ids) {
			return fmt.Errorf("embeddocs %s length mismatch: batch=%d vecs=%d ids=%d", model, len(batch), len(vecs), len(ids))
		}
		vdb.Flush()
		w.MarkOld(batchLast)
		w.Flush()
		batch = nil
		ids = nil
		return nil
	}

	for d := range w.Recent() {
		lg.Debug("embeddocs sync start", "model", model, "doc", d.ID)
		batch = append(batch, llm.EmbedDoc{Title: d.Title, Text: d.Text})
		ids = append(ids, d.ID)
		batchLast = d.DBTime
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if len(batch) > 0 {
		// More to flush, but flush uses w.MarkOld,
		// which has to be called during an iteration over w.Recent.
		// Start a new iteration just to call flush and then break out.
		for _ = range w.Recent() {
			if err := flush(); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// Latest returns the latest known DBTime marked old by the corpus's Watcher.
func Latest(dc *docs.Corpus, embed llm.Embedder) timed.DBTime {
	return dc.DocWatcher(watcherKey(embed.EmbeddingModel())).Latest()
}

// watcherKey returns the watcher key for the given model.
func watcherKey(model string) string {
	// Special case: we embedded everything using text-embedding-004
	// without the model suffix on the watcher key.
	// TODO: Remove when we stop using text-embedding-004.
	if model == "text-embedding-004/768" {
		return "embeddocs"
	}

	return "embeddocs/" + model
}
