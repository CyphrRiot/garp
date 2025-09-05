package search

import (
	"context"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// FileWalker handles concurrent file discovery with type filtering
type FileWalker struct {
	documentTypes map[string]bool
	codeTypes     map[string]bool
	includeCode   bool
	maxWorkers    int
}

// WalkResult contains the results of file walking
type WalkResult struct {
	Files []string
	Count int64
	Error error
}

// NewFileWalker creates a new file walker with specified file types
func NewFileWalker(documentTypes, codeTypes []string, includeCode bool) *FileWalker {
	fw := &FileWalker{
		documentTypes: make(map[string]bool),
		codeTypes:     make(map[string]bool),
		includeCode:   includeCode,
		maxWorkers:    runtime.NumCPU() * 2,
	}

	// Build document type map for O(1) lookup
	for _, ext := range documentTypes {
		fw.documentTypes["."+ext] = true
	}

	// Build code type map for O(1) lookup
	if includeCode {
		for _, ext := range codeTypes {
			fw.codeTypes["."+ext] = true
		}
	}

	return fw
}

// isValidFileType checks if a file extension is in our target types
func (fw *FileWalker) isValidFileType(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))

	// Check document types
	if fw.documentTypes[ext] {
		return true
	}

	// Check code types if enabled
	if fw.includeCode && fw.codeTypes[ext] {
		return true
	}

	// Handle files without extensions (common in config files)
	if ext == "" {
		basename := strings.ToLower(filepath.Base(path))
		// Common extensionless files
		extensionless := []string{
			"readme", "license", "changelog", "dockerfile",
			"makefile", "gitignore", "gitconfig",
		}
		for _, name := range extensionless {
			if basename == name || strings.HasPrefix(basename, name) {
				return true
			}
		}
	}

	return false
}

// shouldSkipDir determines if we should skip a directory
func (fw *FileWalker) shouldSkipDir(name string) bool {
	skipDirs := map[string]bool{
		".git":          true,
		".svn":          true,
		".hg":           true,
		"node_modules":  true,
		".vscode":       true,
		".idea":         true,
		"__pycache__":   true,
		".pytest_cache": true,
		"vendor":        true,
		"target":        true,
		"build":         true,
		"dist":          true,
		".next":         true,
		".nuxt":         true,
		"coverage":      true,
	}

	return skipDirs[name] || strings.HasPrefix(name, ".")
}

// CountFiles counts all matching files efficiently
func (fw *FileWalker) CountFiles(rootPath string) (int64, error) {
	var count int64

	err := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}

		// Skip hidden and excluded directories
		if d.IsDir() {
			if fw.shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// Check if this is a file type we care about
		if fw.isValidFileType(path) {
			atomic.AddInt64(&count, 1)
		}

		return nil
	})

	return count, err
}

// FindFiles finds all matching files using concurrent workers
func (fw *FileWalker) FindFiles(rootPath string) ([]string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Channel for found files
	fileChan := make(chan string, 1000)
	var wg sync.WaitGroup

	// Start file walking in a separate goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(fileChan)

		filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // Skip files we can't access
			}

			// Check for cancellation
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			// Skip hidden and excluded directories
			if d.IsDir() {
				if fw.shouldSkipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}

			// Check if this is a file type we care about
			if fw.isValidFileType(path) {
				select {
				case fileChan <- path:
				case <-ctx.Done():
					return ctx.Err()
				}
			}

			return nil
		})
	}()

	// Collect results
	var files []string
	var mu sync.Mutex

	// Start workers to collect files
	numWorkers := fw.maxWorkers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localFiles := make([]string, 0, 100)

			for file := range fileChan {
				localFiles = append(localFiles, file)

				// Batch append to reduce lock contention
				if len(localFiles) >= 100 {
					mu.Lock()
					files = append(files, localFiles...)
					mu.Unlock()
					localFiles = localFiles[:0]
				}
			}

			// Append remaining files
			if len(localFiles) > 0 {
				mu.Lock()
				files = append(files, localFiles...)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	return files, nil
}

// FindFilesWithPattern finds files containing a specific pattern
func (fw *FileWalker) FindFilesWithPattern(rootPath, pattern string, maxResults int) ([]string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create matcher for the pattern
	matcher := NewWordMatcher([]string{pattern}, true) // case insensitive

	// Channel for candidate files
	fileChan := make(chan string, 500)
	resultChan := make(chan string, maxResults)

	var wg sync.WaitGroup

	// Start file walking
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(fileChan)

		filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if d.IsDir() {
				if fw.shouldSkipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}

			if fw.isValidFileType(path) {
				select {
				case fileChan <- path:
				case <-ctx.Done():
					return ctx.Err()
				}
			}

			return nil
		})
	}()

	// Start search workers
	numWorkers := fw.maxWorkers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for filePath := range fileChan {
				// Quick check if we have enough results
				select {
				case <-ctx.Done():
					return
				default:
				}

				if len(resultChan) >= maxResults {
					continue
				}

				// Check if file contains the pattern
				if matcher.FileContainsWords(filePath, []string{pattern}) {
					select {
					case resultChan <- filePath:
					case <-ctx.Done():
						return
					default:
						// Channel full, continue
					}
				}
			}
		}()
	}

	// Close result channel when all workers done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	var results []string
	for result := range resultChan {
		results = append(results, result)
		if len(results) >= maxResults {
			cancel() // Stop further processing
			break
		}
	}

	return results, nil
}
