package search

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SearchResult represents a file that matches all search criteria
type SearchResult struct {
	FilePath     string
	FileSize     int64
	Excerpts     []string
	CleanContent string
	EmailDate    string
	EmailSubject string
}

// ProgressFunc is an optional callback to report progress like: processed, total, path
type ProgressFunc func(processed, total int, path string)

// SearchEngine handles the multi-word search logic
type SearchEngine struct {
	SearchWords  []string
	ExcludeWords []string
	FileTypes    []string
	IncludeCode  bool
	Registry     *ExtractorRegistry
	Distance     int
	Silent       bool

	// Optional progress callback (nil if unused)
	OnProgress ProgressFunc
}

// NewSearchEngine creates a new search engine instance
func NewSearchEngine(searchWords, excludeWords []string, fileTypes []string, includeCode bool) *SearchEngine {
	return &SearchEngine{
		SearchWords:  searchWords,
		ExcludeWords: excludeWords,
		FileTypes:    fileTypes,
		IncludeCode:  includeCode,
		Registry:     NewExtractorRegistry(),
		Distance:     5000,
		Silent:       false,
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
	// Emit initial progress with 0 processed
	if se.OnProgress != nil {
		se.OnProgress(0, fileCount, "")
	}

	if !se.Silent {
		fmt.Printf("Document files to search: %s\n", formatNumber(fileCount))
	}

	// Step 2: Find files containing the first word
	if !se.Silent {
		fmt.Printf("Finding files with '%s'...\n", se.SearchWords[0])
	}
	candidateFiles, err := FindFilesWithFirstWordProgress(se.SearchWords[0], se.FileTypes, func(processed, total int, path string) {
		if se.OnProgress != nil {
			se.OnProgress(processed, total, path)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("failed to find files with first word: %w", err)
	}
	// If we have fewer candidates than the initial file count, update the total baseline
	total := fileCount
	if len(candidateFiles) > 0 && len(candidateFiles) < fileCount {
		total = len(candidateFiles)
	}

	if len(candidateFiles) == 0 {
		if se.OnProgress != nil {
			se.OnProgress(0, fileCount, "")
		}
		if !se.Silent {
			fmt.Printf("No files found containing '%s'\n", se.SearchWords[0])
		}
		return nil, nil
	}

	if !se.Silent {
		fmt.Printf("Found %s files containing '%s'\n", formatNumber(len(candidateFiles)), se.SearchWords[0])
	}

	// Step 3: Filter files that contain ALL words and don't contain exclude words
	if !se.Silent {
		fmt.Println("Filtering for files containing ALL words...")
	}

	// Separate excludes into extensions and words
	var extExcludes []string
	var wordExcludes []string
	for _, exclude := range se.ExcludeWords {
		if strings.HasPrefix(exclude, ".") {
			extExcludes = append(extExcludes, exclude)
		} else {
			wordExcludes = append(wordExcludes, exclude)
		}
	}

	// Print exclusion info
	if !se.Silent {
		if len(extExcludes) > 0 {
			fmt.Printf("Excluding types: %s\n", strings.Join(extExcludes, ", "))
		}
		if len(wordExcludes) > 0 {
			fmt.Printf("Excluding words: %s\n", strings.Join(wordExcludes, ", "))
		}
	}

	var matchingFiles []string
	processed := 0

	for _, filePath := range candidateFiles {
		processed++
		// Emit per-file progress (filtered phase)
		if se.OnProgress != nil {
			se.OnProgress(processed, total, filePath)
		}

		// Show progress every 500 files
		if processed%500 == 0 && !se.Silent {
			elapsed := time.Since(startTime).Seconds()
			percent := float64(processed) * 100.0 / float64(len(candidateFiles))
			fmt.Printf("Progress: %d/%d files (%.1f%%) - %.0fs elapsed\n",
				processed, len(candidateFiles), percent, elapsed)
		}

		// Check for excluded extensions
		ext := filepath.Ext(filePath)
		excludeByExt := false
		for _, exclude := range extExcludes {
			if ext == exclude {
				excludeByExt = true
				break
			}
		}
		if excludeByExt {
			continue
		}

		// Removed .msg prefilter skip to avoid false negatives

		// Fast prefilter for text files: require presence of the second word before heavy checks
		if len(se.SearchWords) > 1 && !IsBinaryFormat(filePath) {
			if !StreamContainsWord(filePath, se.SearchWords[1]) {
				continue
			}
		}
		// Check if file contains all search words
		hasAllWords := true
		if len(se.SearchWords) > 1 {
			if IsBinaryFormat(filePath) {
				// For binary files, extract text first
				content, _, err := GetFileContent(filePath)
				if err != nil {
					fmt.Printf("Warning: Error reading file %s: %v\n", filePath, err)
					continue
				}
				ext := filepath.Ext(filePath)
				if extractor, exists := se.Registry.GetExtractor(ext); exists {
					extractedText, err := extractor.ExtractText([]byte(content))
					if err != nil {
						if !se.Silent {
							fmt.Printf("Warning: Error extracting text from %s: %v\n", filePath, err)
						}
						continue
					}
					hasAllWords = CheckTextContainsAllWords(CleanContent(extractedText), se.SearchWords, se.Distance)
				} else {
					if !se.Silent {
						fmt.Printf("Warning: No extractor for %s\n", ext)
					}
					continue
				}
			} else {
				hasAllWords, err = CheckFileContainsAllWords(filePath, se.SearchWords, se.Distance, se.Silent)
				if err != nil {
					if !se.Silent {
						fmt.Printf("Warning: Error checking file %s: %v\n", filePath, err)
					}
					continue
				}
			}
		} else {
			// Single-word presence check
			word := se.SearchWords[0]
			if IsBinaryFormat(filePath) {
				// For binaries (including .eml/.msg), extract and run whole-word check on cleaned text
				rawContent, _, err := GetFileContent(filePath)
				if err != nil {
					if !se.Silent {
						fmt.Printf("Warning: Error reading file %s: %v\n", filePath, err)
					}
					continue
				}
				ext := filepath.Ext(filePath)
				if extractor, exists := se.Registry.GetExtractor(ext); exists {
					extractedText, err := extractor.ExtractText([]byte(rawContent))
					if err != nil {
						if !se.Silent {
							fmt.Printf("Warning: Error extracting text from %s: %v\n", filePath, err)
						}
						continue
					}
					hasAllWords = CheckTextContainsAllWords(CleanContent(extractedText), []string{word}, se.Distance)
				} else {
					if !se.Silent {
						fmt.Printf("Warning: No extractor for %s\n", ext)
					}
					continue
				}
			} else {
				// Text files: quick presence check
				hasAllWords, err = CheckFileContainsAllWords(filePath, []string{word}, se.Distance, se.Silent)
				if err != nil {
					if !se.Silent {
						fmt.Printf("Warning: Error checking file %s: %v\n", filePath, err)
					}
					continue
				}
			}
		}

		if !hasAllWords {
			continue
		}

		// Check if file contains any exclude words
		hasExcludeWords := false
		if IsBinaryFormat(filePath) {
			// For binary files, use extracted text
			content, _, err := GetFileContent(filePath)
			if err != nil {
				if !se.Silent {
					fmt.Printf("Warning: Error reading file %s: %v\n", filePath, err)
				}
				continue
			}
			ext := filepath.Ext(filePath)
			if extractor, exists := se.Registry.GetExtractor(ext); exists {
				extractedText, err := extractor.ExtractText([]byte(content))
				if err != nil {
					if !se.Silent {
						fmt.Printf("Warning: Error extracting text from %s: %v\n", filePath, err)
					}
					continue
				}
				hasExcludeWords = CheckTextContainsExcludeWords(CleanContent(extractedText), wordExcludes)
			} else {
				if !se.Silent {
					fmt.Printf("Warning: No extractor for %s\n", ext)
				}
				continue
			}
		} else {
			hasExcludeWords, err = CheckFileContainsExcludeWords(filePath, wordExcludes)
			if err != nil {
				if !se.Silent {
					fmt.Printf("Warning: Error checking exclude words in %s: %v\n", filePath, err)
				}
				continue
			}
		}

		if hasExcludeWords {
			continue
		}

		matchingFiles = append(matchingFiles, filePath)
	}

	if len(matchingFiles) == 0 {
		if !se.Silent {
			fmt.Println("No files found containing all search terms.")
		}
		return nil, nil
	}

	// Step 4: Extract content and create results
	if !se.Silent {
		fmt.Printf("Found %s files containing all words. Extracting content...\n", formatNumber(len(matchingFiles)))
	}

	results := make([]SearchResult, 0, len(matchingFiles))

	for _, filePath := range matchingFiles {
		var content string
		var fileSize int64
		var err error
		var emailDate, emailSubject string

		if IsBinaryFormat(filePath) {
			// For binary files, extract text
			rawContent, size, err := GetFileContent(filePath)
			if err != nil {
				if !se.Silent {
					fmt.Printf("Warning: Error reading file %s: %v\n", filePath, err)
				}
				continue
			}
			fileSize = size
			ext := filepath.Ext(filePath)

			// Best-effort email metadata for EML/MSG from raw headers (without heavy parsing)
			if strings.EqualFold(ext, ".eml") || strings.EqualFold(ext, ".msg") {
				if m := regexp.MustCompile(`(?mi)^Date:\s*(.+)$`).FindStringSubmatch(rawContent); m != nil {
					emailDate = strings.TrimSpace(m[1])
				}
				if m := regexp.MustCompile(`(?mi)^Subject:\s*(.+)$`).FindStringSubmatch(rawContent); m != nil {
					emailSubject = strings.TrimSpace(m[1])
				}
			}

			if extractor, exists := se.Registry.GetExtractor(ext); exists {
				content, err = extractor.ExtractText([]byte(rawContent))
				if err != nil {
					if !se.Silent {
						fmt.Printf("Warning: Error extracting text from %s: %v\n", filePath, err)
					}
					continue
				}
			} else {
				if !se.Silent {
					fmt.Printf("Warning: No extractor for %s\n", ext)
				}
				continue
			}
		} else {
			content, fileSize, err = GetFileContent(filePath)
			if err != nil {
				if !se.Silent {
					fmt.Printf("Warning: Error reading file %s: %v\n", filePath, err)
				}
				continue
			}
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
			FilePath:     filePath,
			FileSize:     fileSize,
			Excerpts:     highlightedExcerpts,
			CleanContent: "",
			EmailDate:    emailDate,
			EmailSubject: emailSubject,
		}

		results = append(results, result)
	}

	totalTime := time.Since(startTime)
	if !se.Silent {
		fmt.Printf("Search completed in %.0f seconds!\n", totalTime.Seconds())
	}

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
