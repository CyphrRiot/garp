package search

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
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
type ProgressFunc func(stage string, processed, total int, path string)

// ConcurrencyManager handles bounded concurrency for heavy operations
type ConcurrencyManager struct {
	sem chan struct{}
}

func NewConcurrencyManager(slots int) *ConcurrencyManager {
	return &ConcurrencyManager{sem: make(chan struct{}, slots)}
}

func (cm *ConcurrencyManager) Acquire() {
	cm.sem <- struct{}{}
}

func (cm *ConcurrencyManager) Release() {
	<-cm.sem
}

func (cm *ConcurrencyManager) ExecuteWithTimeout(fn func(), timeout time.Duration) error {
	done := make(chan struct{})

	go func() {
		defer func() { _ = recover() }()
		fn()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("operation timed out")
	}
}

// heavySem is a single-slot semaphore to bound concurrent binary extractions
var heavySem = make(chan struct{}, 1)

// SearchEngine handles the multi-word search logic
type SearchEngine struct {
	SearchWords       []string
	ExcludeWords      []string
	FileTypes         []string
	IncludeCode       bool
	Registry          *ExtractorRegistry
	Distance          int
	Silent            bool
	HeavyConcurrency  int
	FileTimeoutBinary time.Duration

	// Optional progress callback (nil if unused)
	OnProgress ProgressFunc
}

// NewSearchEngine creates a new search engine instance
func NewSearchEngine(searchWords, excludeWords []string, fileTypes []string, includeCode bool, heavyConcurrency int, fileTimeoutBinary int) *SearchEngine {
	return &SearchEngine{
		SearchWords:       searchWords,
		ExcludeWords:      excludeWords,
		FileTypes:         fileTypes,
		IncludeCode:       includeCode,
		Registry:          NewExtractorRegistry(),
		Distance:          5000,
		Silent:            false,
		HeavyConcurrency:  heavyConcurrency,
		FileTimeoutBinary: time.Duration(fileTimeoutBinary) * time.Millisecond,
	}
}

// DiscoverCandidates finds files containing the first search word
func (se *SearchEngine) DiscoverCandidates(fileCount int) ([]string, int, error) {
	if !se.Silent {
		fmt.Printf("Finding files with '%s'...\n", se.SearchWords[0])
	}
	candidateFiles, err := FindFilesWithFirstWordProgress(se.SearchWords[0], se.FileTypes, func(processed, total int, path string) {
		if se.OnProgress != nil {
			se.OnProgress("discovery", processed, total, path)
		}
	})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to find files with first word: %w", err)
	}
	total := fileCount
	if len(candidateFiles) > 0 && len(candidateFiles) < fileCount {
		total = len(candidateFiles)
	}
	if len(candidateFiles) == 0 {
		if !se.Silent {
			fmt.Printf("No files found containing '%s'\n", se.SearchWords[0])
		}
		return nil, total, nil
	}
	if !se.Silent {
		fmt.Printf("Found %s files containing '%s'\n", formatNumber(len(candidateFiles)), se.SearchWords[0])
	}
	return candidateFiles, total, nil
}

// FilterCandidates filters candidates for all words and excludes
func (se *SearchEngine) FilterCandidates(candidateFiles []string, total int, startTime time.Time) ([]string, error) {
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
	cm := NewConcurrencyManager(se.HeavyConcurrency)

	for _, filePath := range candidateFiles {
		processed++
		// Emit per-file progress (filtered phase)
		if se.OnProgress != nil {
			se.OnProgress("processing", processed, total, filePath)
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
		excludeByExt = slices.Contains(extExcludes, ext)
		if excludeByExt {
			continue
		}

		// Fast prefilter for text files: require presence of the second word before heavy checks
		if len(se.SearchWords) > 1 && !IsBinaryFormat(filePath) {
			if !StreamContainsWord(filePath, se.SearchWords[1]) {
				continue
			}
			// Rarest-terms (heuristic) prefilter for 3+ terms: pick two longest terms as proxies for rarity
			if len(se.SearchWords) >= 3 {
				terms := make([]string, len(se.SearchWords))
				copy(terms, se.SearchWords)
				sort.Slice(terms, func(i, j int) bool { return len(terms[i]) > len(terms[j]) })
				rare := terms[:2]
				if !StreamContainsAllWords(filePath, rare) {
					continue
				}
			}
		}

		// Check if file contains all search words
		hasAllWords := true
		if len(se.SearchWords) > 1 {
			if IsBinaryFormat(filePath) {
				ext := filepath.Ext(filePath)
				// Bounded streaming prefilter for supported binary types (EML/MSG/MBOX/RTF).
				// Skip only when conclusively absent; proceed when undecided.
				if ok, decided := BinaryStreamingPrefilterDecided(filePath, se.SearchWords, 1*1024*1024); decided && !ok {
					continue
				}
				// For binary files, extract text first
				content, _, err := GetFileContent(filePath)
				if err != nil {
					fmt.Printf("Warning: Error reading file %s: %v\n", filePath, err)
					continue
				}
				if extractor, exists := se.Registry.GetExtractor(ext); exists {
					var extractedText string
					var extErr error
					err := cm.ExecuteWithTimeout(func() {
						extractedText, extErr = extractor.ExtractText([]byte(content))
					}, se.FileTimeoutBinary)
					if err != nil || extErr != nil {
						if !se.Silent {
							if extErr != nil {
								fmt.Printf("Warning: Error extracting text from %s: %v\n", filePath, extErr)
							} else {
								fmt.Printf("Warning: Extraction timeout for %s\n", filePath)
							}
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
				ok, err := CheckFileContainsAllWords(filePath, se.SearchWords, se.Distance, se.Silent)
				if err != nil {
					if !se.Silent {
						fmt.Printf("Warning: Error checking file %s: %v\n", filePath, err)
					}
					continue
				}
				hasAllWords = ok
			}
		} else {
			// Single-word presence check
			word := se.SearchWords[0]
			if IsBinaryFormat(filePath) {
				rawContent, _, err := GetFileContent(filePath)
				if err != nil {
					if !se.Silent {
						fmt.Printf("Warning: Error reading file %s: %v\n", filePath, err)
					}
					continue
				}
				ext := filepath.Ext(filePath)
				if extractor, exists := se.Registry.GetExtractor(ext); exists {
					var extractedText string
					var extErr error
					err := cm.ExecuteWithTimeout(func() {
						extractedText, extErr = extractor.ExtractText([]byte(rawContent))
					}, se.FileTimeoutBinary)
					if err != nil || extErr != nil {
						if !se.Silent {
							if extErr != nil {
								fmt.Printf("Warning: Error extracting text from %s: %v\n", filePath, extErr)
							} else {
								fmt.Printf("Warning: Extraction timeout for %s\n", filePath)
							}
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
				ok, err := CheckFileContainsAllWords(filePath, []string{word}, se.Distance, se.Silent)
				if err != nil {
					if !se.Silent {
						fmt.Printf("Warning: Error checking file %s: %v\n", filePath, err)
					}
					continue
				}
				hasAllWords = ok
			}
		}

		if !hasAllWords {
			continue
		}

		// Check if file contains any exclude words
		hasExcludeWords := false
		if IsBinaryFormat(filePath) {
			// For binary files, extract text
			rawContent, _, err := GetFileContent(filePath)
			if err != nil {
				if !se.Silent {
					fmt.Printf("Warning: Error reading file %s: %v\n", filePath, err)
				}
				continue
			}
			ext := filepath.Ext(filePath)
			if extractor, exists := se.Registry.GetExtractor(ext); exists {
				var out string
				var extErr error
				err := cm.ExecuteWithTimeout(func() {
					out, extErr = extractor.ExtractText([]byte(rawContent))
				}, se.FileTimeoutBinary)
				if err != nil || extErr != nil {
					if !se.Silent {
						if extErr != nil {
							fmt.Printf("Warning: Error extracting text from %s: %v\n", filePath, extErr)
						} else {
							fmt.Printf("Warning: Extraction timeout for %s\n", filePath)
						}
					}
					continue
				}
				// Compute exclude words from extracted text (cleaned)
				hasExcludeWords = CheckTextContainsExcludeWords(CleanContent(out), wordExcludes)
			} else {
				if !se.Silent {
					fmt.Printf("Warning: No extractor for %s\n", ext)
				}
				continue
			}
		} else {
			ok2, err := CheckFileContainsExcludeWords(filePath, wordExcludes)
			if err != nil {
				if !se.Silent {
					fmt.Printf("Warning: Error checking exclude words in %s: %v\n", filePath, err)
				}
				continue
			}
			hasExcludeWords = ok2
		}

		if hasExcludeWords {
			continue
		}

		matchingFiles = append(matchingFiles, filePath)
	}
	return matchingFiles, nil
}

// ExtractAndBuildResults extracts content and builds search results
func (se *SearchEngine) ExtractAndBuildResults(matchingFiles []string) ([]SearchResult, error) {
	results := make([]SearchResult, 0, len(matchingFiles))
	cm := NewConcurrencyManager(se.HeavyConcurrency)

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
				err = cm.ExecuteWithTimeout(func() {
					content, err = extractor.ExtractText([]byte(rawContent))
				}, se.FileTimeoutBinary)
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
	return results, nil
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
		se.OnProgress("discovery", 0, fileCount, "")
	}

	if !se.Silent {
		fmt.Printf("Document files to search: %s\n", formatNumber(fileCount))
	}

	// Step 2: Discover candidates
	candidateFiles, total, err := se.DiscoverCandidates(fileCount)
	if err != nil {
		return nil, err
	}
	if candidateFiles == nil {
		if se.OnProgress != nil {
			se.OnProgress("discovery", 0, fileCount, "")
		}
		return nil, nil
	}

	// Step 3: Filter candidates
	matchingFiles, err := se.FilterCandidates(candidateFiles, total, startTime)
	if err != nil {
		return nil, err
	}
	if len(matchingFiles) == 0 {
		if !se.Silent {
			fmt.Println("No files found containing all search terms.")
		}
		return nil, nil
	}

	// Step 4: Extract and build results
	if !se.Silent {
		fmt.Printf("Found %s files containing all words. Extracting content...\n", formatNumber(len(matchingFiles)))
	}

	results, err := se.ExtractAndBuildResults(matchingFiles)
	if err != nil {
		return nil, err
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
