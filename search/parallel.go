package search

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// ParallelSearchEngine orchestrates high-performance parallel searches
type ParallelSearchEngine struct {
	matcher     *WordMatcher
	walker      *FileWalker
	maxWorkers  int
	bufferSize  int
	resultLimit int
}

// SearchJob represents a single file search task
type SearchJob struct {
	FilePath string
	Words    []string
	JobID    int64
}

// ParallelResult contains results from parallel search operations
type ParallelResult struct {
	FilePath    string
	Matches     []MatchResult
	FileSize    int64
	ProcessTime time.Duration
	Error       error
}

// SearchStats tracks performance metrics
type SearchStats struct {
	FilesProcessed int64
	FilesMatched   int64
	TotalBytes     int64
	ElapsedTime    time.Duration
	ThroughputMBs  float64
}

// NewParallelSearchEngine creates a new parallel search engine
func NewParallelSearchEngine(documentTypes, codeTypes []string, includeCode bool) *ParallelSearchEngine {
	numCPU := runtime.NumCPU()

	return &ParallelSearchEngine{
		matcher:     NewWordMatcher([]string{}, true), // Will be updated per search
		walker:      NewFileWalker(documentTypes, codeTypes, includeCode),
		maxWorkers:  numCPU * 2, // I/O bound workload
		bufferSize:  1000,
		resultLimit: 10000,
	}
}

// SetWorkerCount allows customizing the number of worker goroutines
func (pse *ParallelSearchEngine) SetWorkerCount(workers int) {
	if workers > 0 {
		pse.maxWorkers = workers
	}
}

// SearchAllFiles performs parallel search across all matching files
func (pse *ParallelSearchEngine) SearchAllFiles(rootPath string, searchWords, excludeWords []string) ([]ParallelResult, *SearchStats, error) {
	startTime := time.Now()

	// Create context for cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize stats
	stats := &SearchStats{}

	// Update matcher with search words
	pse.matcher = NewWordMatcher(searchWords, true)
	excludeMatcher := NewWordMatcher(excludeWords, true)

	// Find all candidate files
	fmt.Println("🔍 Discovering files...")
	files, err := pse.walker.FindFiles(rootPath)
	if err != nil {
		return nil, stats, fmt.Errorf("failed to find files: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("No matching files found")
		return nil, stats, nil
	}

	fmt.Printf("📁 Found %s files to search\n", formatNumber(len(files)))

	// Create channels
	jobChan := make(chan SearchJob, pse.bufferSize)
	resultChan := make(chan ParallelResult, pse.bufferSize)

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < pse.maxWorkers; i++ {
		wg.Add(1)
		go pse.worker(ctx, jobChan, resultChan, &wg, searchWords, excludeWords, excludeMatcher)
	}

	// Start result collector
	var results []ParallelResult
	var resultWg sync.WaitGroup
	resultWg.Add(1)

	go func() {
		defer resultWg.Done()
		for result := range resultChan {
			atomic.AddInt64(&stats.FilesProcessed, 1)
			atomic.AddInt64(&stats.TotalBytes, result.FileSize)

			if len(result.Matches) > 0 || result.Error == nil {
				if len(result.Matches) > 0 {
					atomic.AddInt64(&stats.FilesMatched, 1)
				}
				results = append(results, result)
			}

			// Progress update every 500 files
			processed := atomic.LoadInt64(&stats.FilesProcessed)
			if processed%500 == 0 {
				elapsed := time.Since(startTime).Seconds()
				throughput := float64(atomic.LoadInt64(&stats.TotalBytes)) / (1024 * 1024) / elapsed
				fmt.Printf("⚡ Progress: %d/%d files (%.1f%%) - %.1f MB/s\n",
					processed, len(files),
					float64(processed)*100.0/float64(len(files)),
					throughput)
			}
		}
	}()

	// Send jobs
	go func() {
		defer close(jobChan)
		for i, filePath := range files {
			select {
			case jobChan <- SearchJob{
				FilePath: filePath,
				Words:    searchWords,
				JobID:    int64(i),
			}:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wait for workers to complete
	wg.Wait()
	close(resultChan)

	// Wait for result collection
	resultWg.Wait()

	// Calculate final stats
	stats.ElapsedTime = time.Since(startTime)
	if stats.ElapsedTime > 0 {
		totalMB := float64(stats.TotalBytes) / (1024 * 1024)
		stats.ThroughputMBs = totalMB / stats.ElapsedTime.Seconds()
	}

	fmt.Printf("✅ Search completed: %d files processed, %d matches found in %.2fs (%.1f MB/s)\n",
		stats.FilesProcessed, stats.FilesMatched, stats.ElapsedTime.Seconds(), stats.ThroughputMBs)

	return results, stats, nil
}

// worker processes search jobs in parallel
func (pse *ParallelSearchEngine) worker(ctx context.Context, jobs <-chan SearchJob, results chan<- ParallelResult, wg *sync.WaitGroup, searchWords, excludeWords []string, excludeMatcher *WordMatcher) {
	defer wg.Done()

	for {
		select {
		case job, ok := <-jobs:
			if !ok {
				return
			}

			startTime := time.Now()
			result := pse.processFile(job, searchWords, excludeWords, excludeMatcher)
			result.ProcessTime = time.Since(startTime)

			select {
			case results <- result:
			case <-ctx.Done():
				return
			}

		case <-ctx.Done():
			return
		}
	}
}

// processFile handles the search logic for a single file
func (pse *ParallelSearchEngine) processFile(job SearchJob, searchWords, excludeWords []string, excludeMatcher *WordMatcher) ParallelResult {
	result := ParallelResult{
		FilePath: job.FilePath,
		Matches:  []MatchResult{},
	}

	// Get file size
	if info, err := getFileInfo(job.FilePath); err == nil {
		result.FileSize = info.Size()
	}

	// First, check if file contains all search words
	if !pse.matcher.FileContainsWords(job.FilePath, searchWords) {
		return result // No matches
	}

	// Check exclude words if specified
	if len(excludeWords) > 0 {
		if excludeMatcher.FileContainsWords(job.FilePath, excludeWords) {
			return result // File contains excluded terms
		}
	}

	// Find detailed matches
	matches, err := pse.matcher.FindWordsInFile(job.FilePath, searchWords)
	if err != nil {
		result.Error = err
		return result
	}

	result.Matches = matches
	return result
}

// SearchFirstWord performs optimized search for files containing the first search word
func (pse *ParallelSearchEngine) SearchFirstWord(rootPath, firstWord string, maxResults int) ([]string, error) {
	return pse.walker.FindFilesWithPattern(rootPath, firstWord, maxResults)
}

// BatchSearch performs searches for multiple word combinations in parallel
func (pse *ParallelSearchEngine) BatchSearch(rootPath string, searches [][]string, excludeWords []string) ([][]ParallelResult, error) {
	results := make([][]ParallelResult, len(searches))
	var wg sync.WaitGroup

	// Limit concurrent batch searches to prevent resource exhaustion
	semaphore := make(chan struct{}, runtime.NumCPU())

	for i, searchWords := range searches {
		wg.Add(1)
		go func(index int, words []string) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			searchResults, _, err := pse.SearchAllFiles(rootPath, words, excludeWords)
			if err != nil {
				fmt.Printf("Warning: Batch search %d failed: %v\n", index, err)
				return
			}

			results[index] = searchResults
		}(i, searchWords)
	}

	wg.Wait()
	return results, nil
}

// OptimizeForFileCount adjusts worker count based on expected file count
func (pse *ParallelSearchEngine) OptimizeForFileCount(fileCount int) {
	// Adjust worker count based on file count and system resources
	optimalWorkers := pse.maxWorkers

	if fileCount < 100 {
		optimalWorkers = 2 // Low overhead for small searches
	} else if fileCount < 1000 {
		optimalWorkers = runtime.NumCPU()
	} else if fileCount > 10000 {
		optimalWorkers = runtime.NumCPU() * 3 // I/O bound with large datasets
	}

	pse.maxWorkers = optimalWorkers
	fmt.Printf("🔧 Optimized worker count: %d workers for %s files\n",
		optimalWorkers, formatNumber(fileCount))
}

// GetEstimatedDuration provides time estimate based on file count and system performance
func (pse *ParallelSearchEngine) GetEstimatedDuration(fileCount int) time.Duration {
	// Base estimates on typical performance characteristics
	baseTimePerFile := time.Millisecond * 10 // 10ms per file baseline

	// Adjust for parallelization efficiency
	parallelEfficiency := float64(pse.maxWorkers) * 0.8 // 80% parallel efficiency

	estimatedMs := float64(fileCount) * float64(baseTimePerFile.Milliseconds()) / parallelEfficiency

	return time.Duration(estimatedMs) * time.Millisecond
}

// Close releases resources used by the parallel search engine
func (pse *ParallelSearchEngine) Close() {
	if pse.matcher != nil {
		pse.matcher.Close()
	}
}
