package overlap

import (
	"context"
	"sync"

	"github.com/Jake-Network/radar/internal/integration"
)

type pairIndices struct{ left, right int }

type inspectedPair struct {
	report   integration.Report
	complete bool
}

// inspectPairBatch performs full analyses in separate private repositories.
// Workers never touch finding attribution or standalone baselines. Their slim
// reports retain input order for deterministic processing after every worker
// has released its private repository, including error and cancellation paths.
func inspectPairBatch(ctx context.Context, count, limit int, inspect func(context.Context, int) (integration.Report, error)) ([]inspectedPair, error) {
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]inspectedPair, count)
	jobs := make(chan int)
	var workers sync.WaitGroup
	var firstFailure sync.Once
	var firstErr error
	for range min(max(1, limit), count) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if workerCtx.Err() != nil {
					return
				}
				report, err := inspect(workerCtx, index)
				if err != nil {
					firstFailure.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
				if workerCtx.Err() == nil {
					results[index] = inspectedPair{report: cloneAnalysis(report), complete: true}
				}
			}
		}()
	}
dispatch:
	for index := 0; index < count; index++ {
		select {
		case jobs <- index:
		case <-workerCtx.Done():
			break dispatch
		}
	}
	close(jobs)
	workers.Wait()
	if firstErr != nil {
		return results, firstErr
	}
	return results, ctx.Err()
}
