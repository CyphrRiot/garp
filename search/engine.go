package search

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"find-words/search/pdf"
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

// enablePDFs gates PDF processing within engine.go; default false preserves current behavior.
var enablePDFs = true

// pdfSem is a single global token to ensure PDF concurrency = 1 without risking hangs.
var pdfSem = make(chan struct{}, 2)

// PDF governor: pacing + budget, synchronous and safe.
// Returns true if this PDF is allowed to proceed now; false when skipped due to budget.
func (se *SearchEngine) pdfGovernorAllow() bool {
	// Budget gating
	if atomic.LoadInt64(&se.pdfBudget) > 0 {
		pro := atomic.LoadInt64(&se.pdfProcessed)
		if pro >= atomic.LoadInt64(&se.pdfBudget) {
			atomic.AddInt64(&se.pdfSkippedBudget, 1)
			return false
		}
	}

	// Pacing (min interval between PDFs)
	if se.pdfMinInterval > 0 {
		last := time.Unix(0, atomic.LoadInt64(&se.pdfLastAt))
		now := time.Now()
		if delta := now.Sub(last); delta < se.pdfMinInterval && !last.IsZero() {
			time.Sleep(se.pdfMinInterval - delta)
		}
		atomic.StoreInt64(&se.pdfLastAt, time.Now().UnixNano())
	}

	// Count this PDF as processed
	atomic.AddInt64(&se.pdfProcessed, 1)
	return true
}

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
	FilterWorkers     int
	FileTimeoutBinary time.Duration

	// PDF governor (defaults: pacing on, no budget)
	pdfMinInterval   time.Duration
	pdfBudget        int64 // 0 = unlimited
	pdfProcessed     int64 // atomic counter
	pdfSkippedBudget int64 // atomic counter
	pdfLastAt        int64 // UnixNano (atomic)

	// Metrics (atomic)
	emlPrefilterCount    int64
	emlPrefilterDurNanos int64
	emlExtractCount      int64
	emlExtractDurNanos   int64
	msgPrefilterCount    int64
	msgPrefilterDurNanos int64
	msgExtractCount      int64
	msgExtractDurNanos   int64

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
		FilterWorkers:     2,
		FileTimeoutBinary: time.Duration(fileTimeoutBinary) * time.Millisecond,

		// PDF governor defaults (safe)
		pdfMinInterval: 0,
		pdfBudget:      0, // unlimited by default
		pdfLastAt:      0, // no pacing history yet
	}
}

// NewSearchEngineWithWorkers creates a new search engine instance with an explicit filter worker count
func NewSearchEngineWithWorkers(searchWords, excludeWords []string, fileTypes []string, includeCode bool, heavyConcurrency int, fileTimeoutBinary int, filterWorkers int) *SearchEngine {
	se := NewSearchEngine(searchWords, excludeWords, fileTypes, includeCode, heavyConcurrency, fileTimeoutBinary)
	if filterWorkers > 0 {
		se.FilterWorkers = filterWorkers
	}
	return se
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

	// Results and synchronization
	var matchingFiles []string
	var mu sync.Mutex

	// Progress (atomic across workers)
	var processed int64

	// Concurrency manager for heavy extraction gating
	cm := NewConcurrencyManager(se.HeavyConcurrency)

	// Worker pool for Stage 2 text filtering
	workers := se.FilterWorkers
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan string, workers*4)
	var wg sync.WaitGroup

	handleOne := func(filePath string) bool {
		// Check for excluded extensions
		ext := filepath.Ext(filePath)
		if slices.Contains(extExcludes, ext) {
			return false
		}

		// Consolidated prefilter for text files: single streaming pass on rarest-two or both terms
		if !IsBinaryFormat(filePath) && len(se.SearchWords) >= 2 {
			termsToCheck := se.SearchWords
			if len(se.SearchWords) >= 3 {
				terms := make([]string, len(se.SearchWords))
				copy(terms, se.SearchWords)
				sort.Slice(terms, func(i, j int) bool { return len(terms[i]) > len(terms[j]) })
				termsToCheck = terms[:2]
			}
			found, decided := StreamContainsAllWordsDecided(filePath, termsToCheck)
			if decided && !found {
				return false
			}
		}

		// Check if file contains all search words
		hasAllWords := true
		if len(se.SearchWords) > 1 {
			if IsBinaryFormat(filePath) {
				ext := filepath.Ext(filePath)

				// PDF presence-only gate (Step 2): enable guarded scan; otherwise remain disabled.
				if strings.EqualFold(ext, ".pdf") {
					// Remain disabled unless explicitly enabled.
					if !enablePDFs {
						return false
					}
					// Global governor: pacing/budget.
					if !se.pdfGovernorAllow() {
						// Skipped due to budget (truthfully counted), do not proceed.
						return false
					}
					// Concurrency = 1 with short timeout to guarantee we never hang.
					tokenTimer := time.NewTimer(50 * time.Millisecond)
					defer tokenTimer.Stop()
					select {
					case pdfSem <- struct{}{}:
						// acquired
					case <-tokenTimer.C:
						// Could not acquire quickly; treat as undecided (do not skip via prefilter here).
						return false
					}
					// Ensure release even if provider panics.
					defer func() { <-pdfSem }()
					// Simple bounded text extraction via pdfcpu helper; undecided on timeout/error.
					hasAllWords = false

					type txtRes struct {
						txt     string
						matched bool
						err     error
					}
					resCh := make(chan txtRes, 1)
					go func() {
						defer func() { _ = recover() }()
						t, m, e := pdf.ExtractAllTextCapped(filePath, 200, 128*1024, se.SearchWords, se.Distance)
						resCh <- txtRes{txt: t, matched: m, err: e}
					}()

					wallTimer := time.NewTimer(250 * time.Millisecond)
					defer wallTimer.Stop()

					var matched bool
					var err error
					select {
					case r := <-resCh:
						matched, err = r.matched, r.err
					case <-wallTimer.C:
						// Timeout: undecided, do not accept based on this.
						return false
					}

					if err != nil {
						// Undecided/error: do not accept based on this.
						return false
					}

					if matched {
						hasAllWords = true
					}
				}

				// Bounded streaming prefilter for supported binary types.
				// EML/MSG use a smaller cap; PDFs and others use a conservative default.
				cap := int64(1024 * 1024)
				if strings.EqualFold(ext, ".eml") || strings.EqualFold(ext, ".msg") {
					cap = int64(256 * 1024)
				}
				startPF := time.Now()
				found, decided := BinaryStreamingPrefilterDecided(filePath, se.SearchWords, cap)
				durPF := time.Since(startPF)
				switch strings.ToLower(ext) {
				case ".eml":
					atomic.AddInt64(&se.emlPrefilterCount, 1)
					atomic.AddInt64(&se.emlPrefilterDurNanos, durPF.Nanoseconds())
				case ".msg":
					atomic.AddInt64(&se.msgPrefilterCount, 1)
					atomic.AddInt64(&se.msgPrefilterDurNanos, durPF.Nanoseconds())
				}

				// Decided negative => safe skip
				if decided && !found {
					return false
				}
				// DISABLED: PDF processing completely disabled to prevent system hangs
				// Never accept PDFs based on prefilter alone
				if strings.EqualFold(ext, ".pdf") && !enablePDFs {
					// Skip all PDF processing to prevent hangs
					return false
				} else {
					// Extract and verify distance for multi-word binaries
					if extractor, exists := se.Registry.GetExtractor(ext); exists {
						content, _, err := GetFileContent(filePath)
						if err != nil {
							if !se.Silent {
								fmt.Printf("Warning: Error reading file %s: %v\n", filePath, err)
							}
							return false
						}
						var extractedText string
						var extErr error
						startXT := time.Now()
						cm.Acquire()
						err = cm.ExecuteWithTimeout(func() {
							extractedText, extErr = extractor.ExtractText([]byte(content))
						}, se.FileTimeoutBinary)
						cm.Release()
						durXT := time.Since(startXT)
						switch strings.ToLower(ext) {
						case ".eml":
							atomic.AddInt64(&se.emlExtractCount, 1)
							atomic.AddInt64(&se.emlExtractDurNanos, durXT.Nanoseconds())
						case ".msg":
							atomic.AddInt64(&se.msgExtractCount, 1)
							atomic.AddInt64(&se.msgExtractDurNanos, durXT.Nanoseconds())
						}
						if err != nil || extErr != nil {
							if !se.Silent {
								if extErr != nil {
									// underlying extractor error
									fmt.Printf("Warning: Error extracting text from %s: %v\n", filePath, extErr)
								} else {
									fmt.Printf("Warning: Extraction timeout for %s\n", filePath)
								}
							}
							return false
						}
						hasAllWords = CheckTextContainsAllWords(CleanContent(extractedText), se.SearchWords, se.Distance)
					} else {
						if !se.Silent {
							fmt.Printf("Warning: No extractor for %s\n", ext)
						}
						return false
					}
				}
			} else {
				// Text file: stream+distance
				ok, err := CheckFileContainsAllWords(filePath, se.SearchWords, se.Distance, se.Silent)
				if err != nil {
					if !se.Silent {
						fmt.Printf("Warning: Error checking file %s: %v\n", filePath, err)
					}
					return false
				}
				hasAllWords = ok
			}
		} else {
			// Single-word presence check
			word := se.SearchWords[0]
			if IsBinaryFormat(filePath) {
				ext := filepath.Ext(filePath)
				// Run bounded prefilter for binary types (honor PDFs to avoid unnecessary extraction)
				cap := int64(1024 * 1024)
				if strings.EqualFold(ext, ".eml") || strings.EqualFold(ext, ".msg") {
					cap = int64(256 * 1024)
				}
				foundPF, decidedPF := BinaryStreamingPrefilterDecided(filePath, []string{word}, cap)
				// Decided negative => safe skip
				if decidedPF && !foundPF {
					return false
				}
				// PDF presence-only gate for single-word (Step 2): guarded, no extraction.
				if strings.EqualFold(ext, ".pdf") {
					if !enablePDFs {
						// Keep disabled behavior: do not accept based on generic prefilter.
					} else {
						// Governor + single concurrency token with short timeout to avoid hangs.
						if !se.pdfGovernorAllow() {
							return false
						}
						tokenTimer := time.NewTimer(50 * time.Millisecond)
						defer tokenTimer.Stop()
						select {
						case pdfSem <- struct{}{}:
							defer func() { <-pdfSem }()
						case <-tokenTimer.C:
							return false
						}
						foundOne, decidedOne := PDFPresenceOnlyPathCapped(filePath, []string{word}, 250, 800*time.Millisecond)
						if decidedOne && !foundOne {
							return false
						}
						if decidedOne && foundOne {
							hasAllWords = true
						}
					}
				} else {
					// Bounded extraction fallback under semaphore + timeout
					rawContent, _, err := GetFileContent(filePath)
					if err != nil {
						if !se.Silent {
							fmt.Printf("Warning: Error reading file %s: %v\n", filePath, err)
						}
						return false
					}
					if extractor, exists := se.Registry.GetExtractor(ext); exists {
						var extractedText string
						var extErr error
						startXT := time.Now()
						cm.Acquire()
						err = cm.ExecuteWithTimeout(func() {
							extractedText, extErr = extractor.ExtractText([]byte(rawContent))
						}, se.FileTimeoutBinary)
						cm.Release()
						durXT := time.Since(startXT)
						switch strings.ToLower(ext) {
						case ".eml":
							atomic.AddInt64(&se.emlExtractCount, 1)
							atomic.AddInt64(&se.emlExtractDurNanos, durXT.Nanoseconds())
						case ".msg":
							atomic.AddInt64(&se.msgExtractCount, 1)
							atomic.AddInt64(&se.msgExtractDurNanos, durXT.Nanoseconds())
						}
						if err != nil || extErr != nil {
							if !se.Silent {
								if extErr != nil {
									fmt.Printf("Warning: Error extracting text from %s: %v\n", filePath, extErr)
								} else {
									fmt.Printf("Warning: Extraction timeout for %s\n", filePath)
								}
							}
							return false
						}
						hasAllWords = CheckTextContainsAllWords(CleanContent(extractedText), []string{word}, se.Distance)
					} else {
						if !se.Silent {
							fmt.Printf("Warning: No extractor for %s\n", ext)
						}
						return false
					}
				}
			} else {
				ok, err := CheckFileContainsAllWords(filePath, []string{word}, se.Distance, se.Silent)
				if err != nil {
					if !se.Silent {
						fmt.Printf("Warning: Error checking file %s: %v\n", filePath, err)
					}
					return false
				}
				hasAllWords = ok
			}
		}

		if !hasAllWords {
			return false
		}

		// Check if file contains any exclude words
		hasExcludeWords := false
		if len(wordExcludes) > 0 && IsBinaryFormat(filePath) {
			// For binary files, extract text (gated and timed)
			rawContent, _, err := GetFileContent(filePath)
			if err != nil {
				if !se.Silent {
					fmt.Printf("Warning: Error reading file %s: %v\n", filePath, err)
				}
				return false
			}
			ext := filepath.Ext(filePath)
			if extractor, exists := se.Registry.GetExtractor(ext); exists {
				var out string
				var extErr error
				cm.Acquire()
				err := cm.ExecuteWithTimeout(func() {
					out, extErr = extractor.ExtractText([]byte(rawContent))
				}, se.FileTimeoutBinary)
				cm.Release()
				if err != nil || extErr != nil {
					if !se.Silent {
						if extErr != nil {
							fmt.Printf("Warning: Error extracting text from %s: %v\n", filePath, extErr)
						} else {
							fmt.Printf("Warning: Extraction timeout for %s\n", filePath)
						}
					}
					return false
				}
				// Compute exclude words from extracted text (cleaned)
				hasExcludeWords = CheckTextContainsExcludeWords(CleanContent(out), wordExcludes)
			} else {
				if !se.Silent {
					fmt.Printf("Warning: No extractor for %s\n", ext)
				}
				return false
			}
		} else if len(wordExcludes) > 0 {
			ok2, err := CheckFileContainsExcludeWords(filePath, wordExcludes)
			if err != nil {
				if !se.Silent {
					fmt.Printf("Warning: Error checking exclude words in %s: %v\n", filePath, err)
				}
				return false
			}
			hasExcludeWords = ok2
		}

		if hasExcludeWords {
			return false
		}

		return true
	}

	// Start workers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for filePath := range jobs {
				matched := handleOne(filePath)

				// Append results if matched
				if matched {
					mu.Lock()
					matchingFiles = append(matchingFiles, filePath)
					mu.Unlock()
				}

				// Atomic progress update
				cur := atomic.AddInt64(&processed, 1)
				if se.OnProgress != nil {
					se.OnProgress("processing", int(cur), total, filePath)
				}
				// Optional periodic console progress
				if cur%500 == 0 && !se.Silent {
					elapsed := time.Since(startTime).Seconds()
					percent := float64(cur) * 100.0 / float64(len(candidateFiles))
					fmt.Printf("Progress: %d/%d files (%.1f%%) - %.0fs elapsed\n",
						cur, len(candidateFiles), percent, elapsed)
				}
			}
		}()
	}

	// Enqueue jobs
	for _, p := range candidateFiles {
		jobs <- p
	}
	close(jobs)
	wg.Wait()

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

		// Clean content and extract excerpts (make excerpt window reflect distance)
		cleanContent := CleanContent(content)
		SetExcerptContextLimit(se.Distance)
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

		// Latency metrics summary (averages in ms)
		if se.emlPrefilterCount > 0 || se.emlExtractCount > 0 || se.msgPrefilterCount > 0 || se.msgExtractCount > 0 {
			fmt.Println("Latency (avg ms):")
			if se.emlPrefilterCount > 0 {
				avg := float64(se.emlPrefilterDurNanos) / 1e6 / float64(se.emlPrefilterCount)
				fmt.Printf("  EML prefilter: %d • %.1fms\n", se.emlPrefilterCount, avg)
			}
			if se.emlExtractCount > 0 {
				avg := float64(se.emlExtractDurNanos) / 1e6 / float64(se.emlExtractCount)
				fmt.Printf("  EML extract:   %d • %.1fms\n", se.emlExtractCount, avg)
			}
			if se.msgPrefilterCount > 0 {
				avg := float64(se.msgPrefilterDurNanos) / 1e6 / float64(se.msgPrefilterCount)
				fmt.Printf("  MSG prefilter: %d • %.1fms\n", se.msgPrefilterCount, avg)
			}
			if se.msgExtractCount > 0 {
				avg := float64(se.msgExtractDurNanos) / 1e6 / float64(se.msgExtractCount)
				fmt.Printf("  MSG extract:   %d • %.1fms\n", se.msgExtractCount, avg)
			}
		}
		// PDF governor summary
		if atomic.LoadInt64(&se.pdfProcessed) > 0 || atomic.LoadInt64(&se.pdfSkippedBudget) > 0 {
			fmt.Printf("  PDF scanned: %d • skipped (budget): %d • pages truncated: %d\n",
				atomic.LoadInt64(&se.pdfProcessed),
				atomic.LoadInt64(&se.pdfSkippedBudget),
				atomic.LoadInt64(&pdfPagesTruncated))
		}
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

// GetPDFStats returns PDF processing counters: processed and skipped due to budget.
func (se *SearchEngine) GetPDFStats() (processed int64, skippedBudget int64) {
	return atomic.LoadInt64(&se.pdfProcessed), atomic.LoadInt64(&se.pdfSkippedBudget)
}

// GetPDFStatsDetailed returns PDF counters including truncated page count for UI/metrics.
func (se *SearchEngine) GetPDFStatsDetailed() (processed int64, skippedBudget int64, truncatedPages int64) {
	return atomic.LoadInt64(&se.pdfProcessed), atomic.LoadInt64(&se.pdfSkippedBudget), atomic.LoadInt64(&pdfPagesTruncated)
}
