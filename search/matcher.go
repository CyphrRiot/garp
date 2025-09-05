package search

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"unicode"
)

// WordMatcher handles efficient word matching with memory optimization
type WordMatcher struct {
	words           []string
	caseInsensitive bool
	bufferPool      *sync.Pool
	maxFileSize     int64
	mmapThreshold   int64
}

// MatchResult contains information about a successful match
type MatchResult struct {
	Word     string
	Position int
	Line     int
}

// NewWordMatcher creates a new optimized word matcher
func NewWordMatcher(words []string, caseInsensitive bool) *WordMatcher {
	wm := &WordMatcher{
		words:           make([]string, len(words)),
		caseInsensitive: caseInsensitive,
		maxFileSize:     50 * 1024 * 1024, // 50MB max
		mmapThreshold:   1024 * 1024,      // Use mmap for files > 1MB
		bufferPool: &sync.Pool{
			New: func() interface{} {
				return make([]byte, 64*1024) // 64KB buffers
			},
		},
	}

	// Prepare words for matching
	for i, word := range words {
		if caseInsensitive {
			wm.words[i] = strings.ToLower(word)
		} else {
			wm.words[i] = word
		}
	}

	return wm
}

// isWordBoundary checks if a character position is a word boundary
func isWordBoundary(data []byte, pos int) bool {
	if pos < 0 || pos >= len(data) {
		return true
	}

	char := rune(data[pos])
	return !unicode.IsLetter(char) && !unicode.IsDigit(char) && char != '_'
}

// findWordInBuffer finds a word in a buffer with word boundary checking
func (wm *WordMatcher) findWordInBuffer(buffer []byte, word string) []MatchResult {
	var results []MatchResult

	searchData := buffer
	searchWord := word

	if wm.caseInsensitive {
		// Convert buffer to lowercase for case-insensitive search
		searchData = bytes.ToLower(buffer)
	}

	wordBytes := []byte(searchWord)
	wordLen := len(wordBytes)

	if wordLen == 0 {
		return results
	}

	// Boyer-Moore-like search with word boundaries
	pos := 0
	line := 1

	for pos < len(searchData) {
		// Find next occurrence
		index := bytes.Index(searchData[pos:], wordBytes)
		if index == -1 {
			break
		}

		absolutePos := pos + index

		// Check word boundaries
		beforeBoundary := absolutePos == 0 || isWordBoundary(searchData, absolutePos-1)
		afterBoundary := absolutePos+wordLen >= len(searchData) || isWordBoundary(searchData, absolutePos+wordLen)

		if beforeBoundary && afterBoundary {
			// Count lines up to this position
			for i := pos; i < absolutePos; i++ {
				if searchData[i] == '\n' {
					line++
				}
			}

			results = append(results, MatchResult{
				Word:     word,
				Position: absolutePos,
				Line:     line,
			})
		}

		pos = absolutePos + 1
	}

	return results
}

// FileContainsWords checks if a file contains all specified words
func (wm *WordMatcher) FileContainsWords(filePath string, words []string) bool {
	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return false
	}

	fileSize := stat.Size()

	// Skip empty files
	if fileSize == 0 {
		return false
	}

	// Skip files that are too large
	if fileSize > wm.maxFileSize {
		return wm.checkLargeFile(file, words, fileSize)
	}

	// Read entire file for smaller files
	buffer := wm.bufferPool.Get().([]byte)
	defer wm.bufferPool.Put(buffer)

	if cap(buffer) < int(fileSize) {
		buffer = make([]byte, fileSize)
	} else {
		buffer = buffer[:fileSize]
	}

	_, err = io.ReadFull(file, buffer)
	if err != nil {
		return false
	}

	return wm.bufferContainsAllWords(buffer, words)
}

// checkLargeFile handles files that are too large to read entirely
func (wm *WordMatcher) checkLargeFile(file *os.File, words []string, fileSize int64) bool {
	// Read first chunk
	chunkSize := int64(5 * 1024 * 1024) // 5MB chunks
	if fileSize < chunkSize {
		chunkSize = fileSize
	}

	buffer := make([]byte, chunkSize)
	_, err := io.ReadFull(file, buffer)
	if err != nil && err != io.ErrUnexpectedEOF {
		return false
	}

	return wm.bufferContainsAllWords(buffer, words)
}

// bufferContainsAllWords checks if buffer contains all words
func (wm *WordMatcher) bufferContainsAllWords(buffer []byte, words []string) bool {
	searchData := buffer

	if wm.caseInsensitive {
		searchData = bytes.ToLower(buffer)
	}

	// Check each word
	for _, word := range words {
		searchWord := word
		if wm.caseInsensitive {
			searchWord = strings.ToLower(word)
		}

		if !wm.bufferContainsWord(searchData, searchWord) {
			return false
		}
	}

	return true
}

// bufferContainsWord checks if buffer contains a specific word with boundaries
func (wm *WordMatcher) bufferContainsWord(buffer []byte, word string) bool {
	wordBytes := []byte(word)
	wordLen := len(wordBytes)

	if wordLen == 0 {
		return true
	}

	pos := 0
	for pos < len(buffer) {
		index := bytes.Index(buffer[pos:], wordBytes)
		if index == -1 {
			return false
		}

		absolutePos := pos + index

		// Check word boundaries
		beforeBoundary := absolutePos == 0 || isWordBoundary(buffer, absolutePos-1)
		afterBoundary := absolutePos+wordLen >= len(buffer) || isWordBoundary(buffer, absolutePos+wordLen)

		if beforeBoundary && afterBoundary {
			return true
		}

		pos = absolutePos + 1
	}

	return false
}

// FindWordsInFile finds all occurrences of words in a file
func (wm *WordMatcher) FindWordsInFile(filePath string, words []string) ([]MatchResult, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	fileSize := stat.Size()
	var results []MatchResult

	if fileSize == 0 {
		return results, nil
	}

	// For large files, process in chunks
	if fileSize > wm.maxFileSize {
		return wm.findInLargeFile(file, words, fileSize)
	}

	// Read entire file
	buffer := make([]byte, fileSize)
	_, err = io.ReadFull(file, buffer)
	if err != nil {
		return nil, err
	}

	// Find all words
	for _, word := range words {
		matches := wm.findWordInBuffer(buffer, word)
		results = append(results, matches...)
	}

	return results, nil
}

// findInLargeFile processes large files in chunks with overlap
func (wm *WordMatcher) findInLargeFile(file *os.File, words []string, fileSize int64) ([]MatchResult, error) {
	var results []MatchResult

	chunkSize := int64(5 * 1024 * 1024) // 5MB chunks
	overlap := int64(1024)              // 1KB overlap for word boundaries

	var offset int64
	lineOffset := 1

	for offset < fileSize {
		readSize := chunkSize
		if offset+readSize > fileSize {
			readSize = fileSize - offset
		}

		buffer := make([]byte, readSize)
		_, err := file.ReadAt(buffer, offset)
		if err != nil && err != io.EOF {
			return results, err
		}

		// Find words in this chunk
		for _, word := range words {
			matches := wm.findWordInBuffer(buffer, word)
			// Adjust line numbers based on offset
			for i := range matches {
				matches[i].Line += lineOffset - 1
			}
			results = append(results, matches...)
		}

		// Count lines in processed chunk for next iteration
		lineOffset += bytes.Count(buffer, []byte{'\n'})

		// Move to next chunk with overlap
		offset += readSize - overlap
		if offset < 0 {
			break
		}
	}

	return results, nil
}

// StreamSearch performs streaming search for very large files
func (wm *WordMatcher) StreamSearch(filePath string, words []string, callback func(MatchResult)) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // 1MB max line length

	lineNum := 1

	for scanner.Scan() {
		line := scanner.Bytes()

		for _, word := range words {
			matches := wm.findWordInBuffer(line, word)
			for _, match := range matches {
				match.Line = lineNum
				callback(match)
			}
		}

		lineNum++
	}

	return scanner.Err()
}

// Close cleans up resources
func (wm *WordMatcher) Close() {
	// Currently no cleanup needed, but reserved for future use
	// (e.g., closing memory-mapped files)
}
