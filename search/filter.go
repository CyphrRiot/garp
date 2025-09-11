package search

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"find-words/config"
)

// CheckTextContainsAllWords checks if extracted text contains all search words
// in any order, within a distance window (in characters) between the earliest
// and latest matched term positions.
func CheckTextContainsAllWords(text string, words []string, distance int) bool {
	if len(words) == 0 {
		return true
	}

	contentStr := strings.ToLower(text)

	// Single-term case: just check presence quickly
	if len(words) == 1 {
		pattern := fmt.Sprintf(`\b%s\b`, regexp.QuoteMeta(strings.ToLower(words[0])))
		regex := regexp.MustCompile(pattern)
		return regex.FindStringIndex(contentStr) != nil
	}

	// Collect positions for each word
	type match struct {
		pos       int
		wordIndex int
	}
	var matches []match
	for i, word := range words {
		pattern := fmt.Sprintf(`\b%s\b`, regexp.QuoteMeta(strings.ToLower(word)))
		regex := regexp.MustCompile(pattern)
		indexes := regex.FindAllStringIndex(contentStr, -1)
		for _, idx := range indexes {
			matches = append(matches, match{pos: idx[0], wordIndex: i})
		}
	}

	if len(matches) == 0 {
		return false
	}

	// Sort all matches by position
	sort.Slice(matches, func(i, j int) bool { return matches[i].pos < matches[j].pos })

	// Sliding window over matches to find a window that covers all words
	counts := make(map[int]int)
	covered := 0
	required := len(words)
	left := 0

	for right := 0; right < len(matches); right++ {
		rw := matches[right].wordIndex
		if counts[rw] == 0 {
			covered++
		}
		counts[rw]++

		// When all words covered, try to shrink from left and check distance
		for covered == required && left <= right {
			window := matches[right].pos - matches[left].pos
			if window <= distance {
				return true
			}
			lw := matches[left].wordIndex
			counts[lw]--
			if counts[lw] == 0 {
				covered--
			}
			left++
		}
	}

	return false
}

// CheckTextContainsExcludeWords checks if extracted text contains any exclude words
func CheckTextContainsExcludeWords(text string, excludeWords []string) bool {
	if len(excludeWords) == 0 {
		return false
	}

	contentStr := strings.ToLower(text)

	// Check each exclude word
	for _, word := range excludeWords {
		if containsWholeWord(contentStr, strings.ToLower(word)) {
			return true
		}
	}

	return false
}

// FileInfo represents information about a file
type FileInfo struct {
	Path string
	Size int64
}

// GetDocumentFileCount returns the count of document files that will be searched (pure Go)
func GetDocumentFileCount(fileTypes []string) (int, error) {
	// Parse allowed extensions from patterns like "-g", "*.txt"
	allowed := make(map[string]bool)
	for i := 0; i < len(fileTypes); i++ {
		if fileTypes[i] == "-g" && i+1 < len(fileTypes) {
			i++
			glob := fileTypes[i]
			if strings.HasPrefix(glob, "*.") {
				ext := strings.ToLower(glob[1:]) // ".txt"
				allowed[ext] = true
			}
		}
	}

	count := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Ignore permission errors; keep walking
			return nil
		}
		if d.IsDir() {
			if d.Name() != "." && config.ShouldSkipDirectory(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if len(allowed) > 0 && !allowed[ext] {
			return nil
		}
		count++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// FindFilesWithFirstWord finds all files containing the first search word (pure Go)
func FindFilesWithFirstWord(word string, fileTypes []string) ([]string, error) {
	// Parse allowed extensions from patterns like "-g", "*.txt"
	allowed := make(map[string]bool)
	for i := 0; i < len(fileTypes); i++ {
		if fileTypes[i] == "-g" && i+1 < len(fileTypes) {
			i++
			glob := fileTypes[i]
			if strings.HasPrefix(glob, "*.") {
				ext := strings.ToLower(glob[1:]) // ".txt"
				allowed[ext] = true
			}
		}
	}

	pattern := fmt.Sprintf(`(?i)\b%s\b`, regexp.QuoteMeta(word))
	re := regexp.MustCompile(pattern)
	heavy := map[string]bool{
		".pdf":  true,
		".docx": true,
		".odt":  true,
		".msg":  true,
	}
	matches := make([]string, 0, 128)
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Ignore permission errors; keep walking
			return nil
		}
		if d.IsDir() {
			if d.Name() != "." && config.ShouldSkipDirectory(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// Filter by extension if provided
		ext := strings.ToLower(filepath.Ext(path))
		if len(allowed) > 0 && !allowed[ext] {
			return nil
		}

		// Fast first-word check: stream file without extraction
		if heavy[ext] {
			// include heavy binary types as candidates; full check later
			matches = append(matches, path)
			return nil
		}

		// Stream up to maxBytes looking for the first word
		const chunkSize = 64 * 1024
		const overlap = 128
		const maxBytes = 10 * 1024 * 1024
		f, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer f.Close()
		var total int64
		prev := make([]byte, 0, overlap)
		buf := make([]byte, chunkSize)
		found := false
		for {
			if total >= maxBytes {
				break
			}
			toRead := chunkSize
			if rem := maxBytes - total; rem < int64(toRead) {
				toRead = int(rem)
			}
			n, rErr := f.Read(buf[:toRead])
			if n > 0 {
				combined := append(prev, buf[:n]...)
				if re.Match(combined) {
					found = true
					break
				}
				if n >= overlap {
					prev = append(prev[:0], buf[n-overlap:n]...)
				} else {
					if len(combined) >= overlap {
						prev = append(prev[:0], combined[len(combined)-overlap:]...)
					} else {
						prev = append(prev[:0], combined...)
					}
				}
				total += int64(n)
			}
			if rErr == io.EOF {
				break
			}
			if rErr != nil {
				break
			}
		}

		if found {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return matches, nil
}

// FindFilesWithFirstWordProgress is like FindFilesWithFirstWord but emits per-file discovery progress.
func FindFilesWithFirstWordProgress(word string, fileTypes []string, onProgress func(processed, total int, path string)) ([]string, error) {
	// Parse allowed extensions from patterns like "-g", "*.txt"
	allowed := make(map[string]bool)
	for i := 0; i < len(fileTypes); i++ {
		if fileTypes[i] == "-g" && i+1 < len(fileTypes) {
			i++
			glob := fileTypes[i]
			if strings.HasPrefix(glob, "*.") {
				ext := strings.ToLower(glob[1:]) // ".txt"
				allowed[ext] = true
			}
		}
	}

	// Estimate total and emit initial progress
	total, _ := GetDocumentFileCount(fileTypes)
	if onProgress != nil {
		onProgress(0, total, "")
	}

	pattern := fmt.Sprintf(`(?i)\b%s\b`, regexp.QuoteMeta(word))
	re := regexp.MustCompile(pattern)
	heavy := map[string]bool{
		".pdf":  true,
		".docx": true,
		".odt":  true,
		".msg":  true,
	}
	matches := make([]string, 0, 128)
	processed := 0

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() != "." && config.ShouldSkipDirectory(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if len(allowed) > 0 && !allowed[ext] {
			return nil
		}

		processed++
		if onProgress != nil {
			onProgress(processed, total, path)
		}

		if heavy[ext] {
			matches = append(matches, path)
			return nil
		}

		const chunkSize = 64 * 1024
		const overlap = 128
		const maxBytes = 10 * 1024 * 1024
		f, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer f.Close()
		var readTotal int64
		prev := make([]byte, 0, overlap)
		buf := make([]byte, chunkSize)
		found := false

		for {
			if readTotal >= maxBytes {
				break
			}
			toRead := chunkSize
			if rem := maxBytes - readTotal; rem < int64(toRead) {
				toRead = int(rem)
			}
			n, rErr := f.Read(buf[:toRead])
			if n > 0 {
				combined := append(prev, buf[:n]...)
				if re.Match(combined) {
					found = true
					break
				}
				if n >= overlap {
					prev = append(prev[:0], buf[n-overlap:n]...)
				} else {
					if len(combined) >= overlap {
						prev = append(prev[:0], combined[len(combined)-overlap:]...)
					} else {
						prev = append(prev[:0], combined...)
					}
				}
				readTotal += int64(n)
			}
			if rErr == io.EOF {
				break
			}
			if rErr != nil {
				break
			}
		}

		if found {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return matches, nil
}

// CheckFileContainsAllWords checks if a file contains all search words
func CheckFileContainsAllWords(filePath string, words []string, distance int, silent bool) (bool, error) {
	content, _, err := GetFileContent(filePath)
	if err != nil {
		return false, err
	}
	return CheckTextContainsAllWords(content, words, distance), nil
}

// CheckFileContainsExcludeWords checks if a file contains any exclude words
func CheckFileContainsExcludeWords(filePath string, excludeWords []string) (bool, error) {
	if len(excludeWords) == 0 {
		return false, nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer file.Close()

	// Get file size for large file handling
	stat, err := file.Stat()
	if err != nil {
		return false, err
	}

	var reader io.Reader = file

	// Limit read size for large files
	if stat.Size() > 50*1024*1024 { // 50MB
		reader = io.LimitReader(file, 10*1024*1024) // Read first 10MB
	} else if stat.Size() > 10*1024*1024 { // 10MB
		reader = io.LimitReader(file, 5*1024*1024) // Read first 5MB
	}

	// Read content
	content, err := io.ReadAll(reader)
	if err != nil {
		return false, err
	}

	contentStr := strings.ToLower(string(content))

	// Check each exclude word
	for _, word := range excludeWords {
		if containsWholeWord(contentStr, strings.ToLower(word)) {
			return true, nil
		}
	}

	return false, nil
}

// GetFileContent reads and returns file content with size limits
func GetFileContent(filePath string) (string, int64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	// Get file size
	stat, err := file.Stat()
	if err != nil {
		return "", 0, err
	}

	var reader io.Reader = file

	// Limit read size for large files
	if stat.Size() > 50*1024*1024 { // 50MB
		reader = io.LimitReader(file, 10*1024*1024) // Read first 10MB
	} else if stat.Size() > 10*1024*1024 { // 10MB
		reader = io.LimitReader(file, 5*1024*1024) // Read first 5MB
	}

	// Read content
	content, err := io.ReadAll(reader)
	if err != nil {
		return "", 0, err
	}

	return string(content), stat.Size(), nil
}

// FormatFileSize formats file size in human readable format
func FormatFileSize(size int64) string {
	const unit = 1024
	if size < unit {
		return strconv.FormatInt(size, 10) + " B"
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(size)/float64(div), 'f', 1, 64) + " " + "KMGTPE"[exp:exp+1] + "B"
}

// StreamContainsWord checks if a file contains a given word using streaming read
func StreamContainsWord(filePath string, word string) bool {
	pattern := fmt.Sprintf(`(?i)\b%s\b`, regexp.QuoteMeta(word))
	re := regexp.MustCompile(pattern)

	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	const chunkSize = 64 * 1024
	const overlap = 128
	const maxBytes = 10 * 1024 * 1024
	var total int64
	prev := make([]byte, 0, overlap)
	buf := make([]byte, chunkSize)
	for {
		if total >= maxBytes {
			break
		}
		toRead := chunkSize
		if rem := maxBytes - total; rem < int64(toRead) {
			toRead = int(rem)
		}
		n, rErr := f.Read(buf[:toRead])
		if n > 0 {
			combined := append(prev, buf[:n]...)
			if re.Match(combined) {
				return true
			}
			if n >= overlap {
				prev = append(prev[:0], buf[n-overlap:n]...)
			} else {
				if len(combined) >= overlap {
					prev = append(prev[:0], combined[len(combined)-overlap:]...)
				} else {
					prev = append(prev[:0], combined...)
				}
			}
			total += int64(n)
		}
		if rErr == io.EOF {
			break
		}
		if rErr != nil {
			break
		}
	}
	return false
}
