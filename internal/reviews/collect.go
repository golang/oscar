// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reviews

import (
	"cmp"
	"context"
	"iter"
	"log/slog"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// CollectChangePreds reads [Change] values from an iterator,
// applies predicates to the values, and collects the results
// into a slice of [ChangePreds] values, skipping changes that
// are rejected.
// The slice is sorted by the predicate default values.
func CollectChangePreds(ctx context.Context, lg *slog.Logger, it iter.Seq[Change], predicates []Predicate, rejects []Reject) []ChangePreds {
	// Applying predicates can be time consuming,
	// and there can be a lot of changes,
	// so shard the work.
	count := runtime.GOMAXPROCS(0)
	chIn := make(chan Change, count)
	chOut := make(chan ChangePreds, count)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(chIn)
		for change := range it {
			select {
			case chIn <- change:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Add(count)
	for range count {
		go func() {
			defer wg.Done()
			for change := range chIn {
				cp, ok, err := ApplyPredicates(ctx, change, predicates, rejects)
				if err != nil {
					// Errors are assumed to be
					// non-critical. Just log them.
					lg.Error("error applying predicate", "change", change.ID(), "err", err)
				}
				if !ok {
					continue
				}

				select {
				case chOut <- cp:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(chOut)
	}()

	var cps []ChangePreds
	for cp := range chOut {
		cps = append(cps, cp)
	}

	total := func(cp ChangePreds) int {
		r := 0
		for _, p := range cp.Predicates {
			r += p.Score
		}
		return r
	}

	slices.SortFunc(cps, func(a, b ChangePreds) int {
		if r := cmp.Compare(total(a), total(b)); r != 0 {
			return -r // negate to sort in descending order
		}
		if r := a.Change.Updated().Compare(b.Change.Updated()); r != 0 {
			return -r // negate to sort newest to oldest
		}

		// Sort change IDs numerically if we can,
		// otherwise lexically.
		aID := a.Change.ID()
		bID := b.Change.ID()
		anum, aerr := strconv.Atoi(aID)
		bnum, berr := strconv.Atoi(bID)
		if aerr == nil && berr == nil {
			return cmp.Compare(anum, bnum)
		} else {
			return strings.Compare(aID, bID)
		}
	})

	return cps
}
