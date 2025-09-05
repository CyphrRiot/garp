package search

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// SearchResult represents a file that matches all search criteria
type SearchResult struct {
	FilePath string
	FileSize int64
	Excerpts []string
}

// SearchEngine handles the multi-word search logic
type SearchEngine struct {
	SearchWords    []string
	ExcludeWords   []string
	DocumentTypes  []string
	CodeTypes      []string
	IncludeCode    bool
	parallelEngine *ParallelSearchEngine
}

// NewSearchEngine creates a new search engine instance
func NewSearchEngine(searchWords, excludeWords []string, documentTypes, codeTypes []string, includeCode bool) *SearchEngine {
	return &SearchEngine{
		SearchWords:    searchWords,
		ExcludeWords:   excludeWords,
		DocumentTypes:  documentTypes,
		CodeTypes:      codeTypes,
		IncludeCode:    includeCode,
		parallelEngine: NewParallelSearchEngine(documentTypes, codeTypes, includeCode),
	}
}

// Execute performs the complete search operation using high-performance parallel processing
func (se *SearchEngine) Execute() ([]SearchResult, error) {
	startTime := time.Now()

	fmt.Printf("🚀 %sHigh-Performance Search Engine%s\n", "\033[1;36m", "\033[0m")
	fmt.Printf("%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", "\033[90m", "\033[0m")

	// Step 1: Get file count estimate
	fileCount, err := GetDocumentFileCount(se.DocumentTypes, se.CodeTypes, se.IncludeCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get file count: %w", err)
	}

	if fileCount == 0 {
		fmt.Println("No matching files found in current directory")
		return nil, nil
	}

	fmt.Printf("📁 Document files to search: %s\n", formatNumber(fileCount))

	// Optimize worker count based on file count
	se.parallelEngine.OptimizeForFileCount(fileCount)

	// Provide time estimate
	estimatedTime := se.parallelEngine.GetEstimatedDuration(fileCount)
	fmt.Printf("⏱️  Estimated time: %v\n", estimatedTime.Round(time.Second))

	// Step 2: Use parallel engine to search all files
	fmt.Printf("🔍 Searching for: %s%s%s\n", "\033[32m", strings.Join(se.SearchWords, " + "), "\033[0m")
	if len(se.ExcludeWords) > 0 {
		fmt.Printf("❌ Excluding: %s%s%s\n", "\033[31m", strings.Join(se.ExcludeWords, " + "), "\033[0m")
	}

	results, stats, err := se.parallelEngine.SearchAllFiles(".", se.SearchWords, se.ExcludeWords)
	if err != nil {
		return nil, fmt.Errorf("parallel search failed: %w", err)
	}

	if len(results) == 0 {
		fmt.Printf("❌ No files found containing all search terms: %s\n", strings.Join(se.SearchWords, " + "))
		return nil, nil
	}

	// Step 3: Convert ParallelResult to SearchResult with content extraction
	fmt.Printf("📄 Processing %s matching files for content extraction...\n", formatNumber(len(results)))

	searchResults := make([]SearchResult, 0, len(results))
	processed := 0

	for _, result := range results {
		if result.Error != nil {
			fmt.Printf("Warning: Error processing file %s: %v\n", result.FilePath, result.Error)
			continue
		}

		// Only process files that have matches
		if len(result.Matches) == 0 {
			continue
		}

		processed++
		if processed%100 == 0 {
			fmt.Printf("💫 Content processing: %d/%d files\n", processed, len(results))
		}

		// Extract content and create excerpts
		content, fileSize, err := GetFileContent(result.FilePath)
		if err != nil {
			fmt.Printf("Warning: Error reading file %s: %v\n", result.FilePath, err)
			continue
		}

		// Clean content and extract meaningful excerpts
		cleanContent := CleanContent(content)
		excerpts := ExtractMeaningfulExcerpts(cleanContent, se.SearchWords, 10)

		// Highlight search terms in excerpts
		highlightedExcerpts := make([]string, len(excerpts))
		for i, excerpt := range excerpts {
			highlightedExcerpts[i] = HighlightTerms(excerpt, se.SearchWords)
		}

		searchResult := SearchResult{
			FilePath: result.FilePath,
			FileSize: fileSize,
			Excerpts: highlightedExcerpts,
		}

		searchResults = append(searchResults, searchResult)
	}

	// Final performance summary
	totalTime := time.Since(startTime)
	fmt.Printf("\n✅ %sSearch Complete!%s\n", "\033[1;32m", "\033[0m")
	fmt.Printf("📊 Performance Summary:\n")
	fmt.Printf("   • Files processed: %s\n", formatNumber(int(stats.FilesProcessed)))
	fmt.Printf("   • Files matched: %s\n", formatNumber(int(stats.FilesMatched)))
	fmt.Printf("   • Total time: %.2fs\n", totalTime.Seconds())
	fmt.Printf("   • Throughput: %.1f MB/s\n", stats.ThroughputMBs)
	fmt.Printf("   • Results ready: %s files with content\n", formatNumber(len(searchResults)))

	return searchResults, nil
}

// ExecuteFirstWordSearch performs optimized search for files containing the first search word
func (se *SearchEngine) ExecuteFirstWordSearch() ([]string, error) {
	if len(se.SearchWords) == 0 {
		return nil, fmt.Errorf("no search words provided")
	}

	fmt.Printf("🎯 Quick search for files containing '%s'...\n", se.SearchWords[0])

	files, err := se.parallelEngine.SearchFirstWord(".", se.SearchWords[0], 10000)
	if err != nil {
		return nil, fmt.Errorf("first word search failed: %w", err)
	}

	fmt.Printf("📁 Found %s files containing '%s'\n", formatNumber(len(files)), se.SearchWords[0])
	return files, nil
}

// GetAbsolutePath returns the absolute path for a file
func GetAbsolutePath(filePath string) string {
	if filepath.IsAbs(filePath) {
		return filePath
	}

	abs, err := filepath.Abs(filePath)
	if err != nil {
		return filePath
	}

	return abs
}

// formatNumber formats a number with thousands separators
func formatNumber(n int) string {
	str := fmt.Sprintf("%d", n)
	if len(str) <= 3 {
		return str
	}

	var result strings.Builder
	for i, digit := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result.WriteString(",")
		}
		result.WriteRune(digit)
	}

	return result.String()
}

// Close releases resources used by the search engine
func (se *SearchEngine) Close() {
	if se.parallelEngine != nil {
		se.parallelEngine.Close()
	}
}

// SetMaxWorkers allows customizing the maximum number of worker goroutines
func (se *SearchEngine) SetMaxWorkers(workers int) {
	if se.parallelEngine != nil {
		se.parallelEngine.SetWorkerCount(workers)
	}
}

// GetSearchStats returns current search statistics
func (se *SearchEngine) GetSearchStats() map[string]interface{} {
	return map[string]interface{}{
		"search_words":   se.SearchWords,
		"exclude_words":  se.ExcludeWords,
		"include_code":   se.IncludeCode,
		"document_types": len(se.DocumentTypes),
		"code_types":     len(se.CodeTypes),
	}
}
