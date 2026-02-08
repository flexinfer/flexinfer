package agentcontext

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ParallelEmbedder provides parallel embedding with worker pools
type ParallelEmbedder struct {
	embedFunc   func(ctx context.Context, texts []string) ([][]float64, error)
	concurrency int
	batchSize   int
	timeout     time.Duration
}

// EmbedBatchResult contains results for a batch
type EmbedBatchResult struct {
	Vectors [][]float64
	Err     error
	Index   int
}

// NewParallelEmbedder creates a new parallel embedder
func NewParallelEmbedder(
	embedFunc func(ctx context.Context, texts []string) ([][]float64, error),
	concurrency int,
	batchSize int,
) *ParallelEmbedder {
	if concurrency <= 0 {
		concurrency = 4
	}
	if batchSize <= 0 {
		batchSize = 64
	}
	return &ParallelEmbedder{
		embedFunc:   embedFunc,
		concurrency: concurrency,
		batchSize:   batchSize,
		timeout:     30 * time.Second,
	}
}

// SetTimeout sets the timeout per batch
func (pe *ParallelEmbedder) SetTimeout(timeout time.Duration) {
	pe.timeout = timeout
}

// EmbedAll embeds all texts in parallel batches
func (pe *ParallelEmbedder) EmbedAll(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// If texts fit in one batch, just embed directly
	if len(texts) <= pe.batchSize {
		return pe.embedFunc(ctx, texts)
	}

	// Split into batches
	var batches [][]string
	for i := 0; i < len(texts); i += pe.batchSize {
		end := i + pe.batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batches = append(batches, texts[i:end])
	}

	// Create result channels
	resultCh := make(chan EmbedBatchResult, len(batches))

	// Worker pool
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, pe.concurrency)

	for i, batch := range batches {
		wg.Add(1)
		go func(idx int, batchTexts []string) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Create timeout context for this batch
			batchCtx, cancel := context.WithTimeout(ctx, pe.timeout)
			defer cancel()

			vectors, err := pe.embedFunc(batchCtx, batchTexts)
			resultCh <- EmbedBatchResult{
				Vectors: vectors,
				Err:     err,
				Index:   idx,
			}
		}(i, batch)
	}

	// Close result channel when all workers done
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results
	results := make([]EmbedBatchResult, 0, len(batches))
	for result := range resultCh {
		results = append(results, result)
	}

	// Sort by index to maintain order
	sort.Slice(results, func(i, j int) bool {
		return results[i].Index < results[j].Index
	})

	// Check for errors and combine vectors
	var allVectors [][]float64
	for _, result := range results {
		if result.Err != nil {
			return nil, fmt.Errorf("batch %d failed: %w", result.Index, result.Err)
		}
		allVectors = append(allVectors, result.Vectors...)
	}

	if len(allVectors) != len(texts) {
		return nil, fmt.Errorf("vector count mismatch: got %d, expected %d", len(allVectors), len(texts))
	}

	return allVectors, nil
}

// EmbedWithRetry embeds texts with retry logic
func (pe *ParallelEmbedder) EmbedWithRetry(ctx context.Context, texts []string, maxRetries int) ([][]float64, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		vectors, err := pe.EmbedAll(ctx, texts)
		if err == nil {
			return vectors, nil
		}
		lastErr = err

		// Exponential backoff
		if attempt < maxRetries {
			delay := time.Duration(1<<uint(attempt)) * 100 * time.Millisecond
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return nil, fmt.Errorf("all retries failed: %w", lastErr)
}

// EmbedStream provides streaming embedding results via a channel
func (pe *ParallelEmbedder) EmbedStream(ctx context.Context, texts []string) <-chan EmbedBatchResult {
	resultCh := make(chan EmbedBatchResult)

	go func() {
		defer close(resultCh)

		if len(texts) == 0 {
			return
		}

		// Split into batches
		var batches [][]string
		for i := 0; i < len(texts); i += pe.batchSize {
			end := i + pe.batchSize
			if end > len(texts) {
				end = len(texts)
			}
			batches = append(batches, texts[i:end])
		}

		// Worker pool
		var wg sync.WaitGroup
		semaphore := make(chan struct{}, pe.concurrency)

		for i, batch := range batches {
			wg.Add(1)
			go func(idx int, batchTexts []string) {
				defer wg.Done()

				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				batchCtx, cancel := context.WithTimeout(ctx, pe.timeout)
				defer cancel()

				vectors, err := pe.embedFunc(batchCtx, batchTexts)

				select {
				case resultCh <- EmbedBatchResult{
					Vectors: vectors,
					Err:     err,
					Index:   idx,
				}:
				case <-ctx.Done():
				}
			}(i, batch)
		}

		wg.Wait()
	}()

	return resultCh
}

// EmbedProgress provides progress updates during embedding
type EmbedProgress struct {
	Total     int
	Completed int
	Failed    int
	Vectors   [][]float64
	Err       error
}

// EmbedWithProgress embeds texts and reports progress
func (pe *ParallelEmbedder) EmbedWithProgress(ctx context.Context, texts []string, progressCh chan<- EmbedProgress) ([][]float64, error) {
	defer close(progressCh)

	if len(texts) == 0 {
		return nil, nil
	}

	// Split into batches
	var batches [][]string
	for i := 0; i < len(texts); i += pe.batchSize {
		end := i + pe.batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batches = append(batches, texts[i:end])
	}

	progress := EmbedProgress{
		Total:   len(batches),
		Vectors: make([][]float64, len(texts)),
	}

	resultCh := make(chan EmbedBatchResult, len(batches))

	// Worker pool
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, pe.concurrency)

	for i, batch := range batches {
		wg.Add(1)
		go func(idx int, batchTexts []string) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			batchCtx, cancel := context.WithTimeout(ctx, pe.timeout)
			defer cancel()

			vectors, err := pe.embedFunc(batchCtx, batchTexts)
			resultCh <- EmbedBatchResult{
				Vectors: vectors,
				Err:     err,
				Index:   idx,
			}
		}(i, batch)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results and report progress
	for result := range resultCh {
		if result.Err != nil {
			progress.Failed++
			progress.Err = result.Err
		} else {
			progress.Completed++
			// Store vectors at correct positions
			startIdx := result.Index * pe.batchSize
			for j, vec := range result.Vectors {
				if startIdx+j < len(progress.Vectors) {
					progress.Vectors[startIdx+j] = vec
				}
			}
		}

		select {
		case progressCh <- progress:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if progress.Err != nil {
		return nil, progress.Err
	}

	return progress.Vectors, nil
}
