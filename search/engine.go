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
	SearchWords  []string
	ExcludeWords []string
	FileTypes    []string
	IncludeCode  bool
}

// NewSearchEngine creates a new search engine instance
func NewSearchEngine(searchWords, excludeWords []string, fileTypes []string, includeCode bool) *SearchEngine {
	return &SearchEngine{
		SearchWords:  searchWords,
		ExcludeWords: excludeWords,
		FileTypes:    fileTypes,
		IncludeCode:  includeCode,
	}
}

// Execute performs the complete search operation
func (se *SearchEngine) Execute() ([]SearchResult, error) {
	startTime := time.Now()
	
	// Step 1: Get file count estimate
	fileCount, err := GetDocumentFileCount(se.FileTypes)
	if err != nil {
		return nil, fmt.Errorf("failed to get file count: %w", err)
	}
	
	fmt.Printf("Document files to search: %s\n", formatNumber(fileCount))
	
	// Step 2: Find files containing the first word
	fmt.Printf("Finding files with '%s'...\n", se.SearchWords[0])
	candidateFiles, err := FindFilesWithFirstWord(se.SearchWords[0], se.FileTypes)
	if err != nil {
		return nil, fmt.Errorf("failed to find files with first word: %w", err)
	}
	
	if len(candidateFiles) == 0 {
		fmt.Printf("No files found containing '%s'\n", se.SearchWords[0])
		return nil, nil
	}
	
	fmt.Printf("Found %s files containing '%s'\n", formatNumber(len(candidateFiles)), se.SearchWords[0])
	
	// Step 3: Filter files that contain ALL words and don't contain exclude words
	fmt.Println("Filtering for files containing ALL words...")
	
	var matchingFiles []string
	processed := 0
	
	for _, filePath := range candidateFiles {
		processed++
		
		// Show progress every 500 files
		if processed%500 == 0 {
			elapsed := time.Since(startTime).Seconds()
			percent := float64(processed) * 100.0 / float64(len(candidateFiles))
			fmt.Printf("Progress: %d/%d files (%.1f%%) - %.0fs elapsed\n", 
				processed, len(candidateFiles), percent, elapsed)
		}
		
		// Check if file contains all search words
		hasAllWords := true
		if len(se.SearchWords) > 1 {
			hasAllWords, err = CheckFileContainsAllWords(filePath, se.SearchWords[1:])
			if err != nil {
				fmt.Printf("Warning: Error checking file %s: %v\n", filePath, err)
				continue
			}
		}
		
		if !hasAllWords {
			continue
		}
		
		// Check if file contains any exclude words
		hasExcludeWords, err := CheckFileContainsExcludeWords(filePath, se.ExcludeWords)
		if err != nil {
			fmt.Printf("Warning: Error checking exclude words in %s: %v\n", filePath, err)
			continue
		}
		
		if hasExcludeWords {
			continue
		}
		
		matchingFiles = append(matchingFiles, filePath)
	}
	
	if len(matchingFiles) == 0 {
		fmt.Println("No files found containing all search terms.")
		return nil, nil
	}
	
	// Step 4: Extract content and create results
	fmt.Printf("Found %s files containing all words. Extracting content...\n", formatNumber(len(matchingFiles)))
	
	results := make([]SearchResult, 0, len(matchingFiles))
	
	for _, filePath := range matchingFiles {
		content, fileSize, err := GetFileContent(filePath)
		if err != nil {
			fmt.Printf("Warning: Error reading file %s: %v\n", filePath, err)
			continue
		}
		
		// Clean content and extract excerpts
		cleanContent := CleanContent(content)
		excerpts := ExtractMeaningfulExcerpts(cleanContent, se.SearchWords, 10)
		
		// Highlight search terms in excerpts
		highlightedExcerpts := make([]string, len(excerpts))
		for i, excerpt := range excerpts {
			highlightedExcerpts[i] = HighlightTerms(excerpt, se.SearchWords)
		}
		
		result := SearchResult{
			FilePath: filePath,
			FileSize: fileSize,
			Excerpts: highlightedExcerpts,
		}
		
		results = append(results, result)
	}
	
	totalTime := time.Since(startTime)
	fmt.Printf("Search completed in %.0f seconds!\n", totalTime.Seconds())
	
	return results, nil
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
