package llmapp

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestAnalyzeRelated(t *testing.T) {
	ctx := context.Background()
	lg := testutil.Slogger(t)
	db := storage.MemDB()

	t.Run("basic", func(t *testing.T) {
		c := New(lg, RelatedTestGenerator(t, 1), db)
		got, err := c.AnalyzeRelated(ctx, doc1, []*Doc{doc2})
		if err != nil {
			t.Fatal(err)
		}
		promptParts := []any{"original", raw1, "related", raw2, docAndRelated.instructions()}
		rawOut, out := relatedTestOutput(t, 1)
		want := &RelatedAnalysis{
			Result: Result{
				Response: rawOut,
				Prompt:   promptParts,
				Schema:   docAndRelated.schema(),
			},
			Output: out,
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("AnalyzeRelated() mismatch (-want +got):\n%s", diff)
		}
	})
}
