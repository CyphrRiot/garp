package search

import (
	"os"
	"strconv"
)

// FileInfo represents information about a file
type FileInfo struct {
	Path string
	Size int64
}

// GetDocumentFileCount returns the count of document files that will be searched
func GetDocumentFileCount(documentTypes, codeTypes []string, includeCode bool) (int, error) {
	walker := NewFileWalker(documentTypes, codeTypes, includeCode)
	count, err := walker.CountFiles(".")
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// FindFilesWithFirstWord finds all files containing the first search word
func FindFilesWithFirstWord(word string, documentTypes, codeTypes []string, includeCode bool) ([]string, error) {
	walker := NewFileWalker(documentTypes, codeTypes, includeCode)

	// Use parallel search to find files containing the word
	files, err := walker.FindFilesWithPattern(".", word, 50000) // Reasonable limit
	if err != nil {
		return nil, err
	}

	return files, nil
}

// CheckFileContainsAllWords checks if a file contains all search words
func CheckFileContainsAllWords(filePath string, words []string) (bool, error) {
	matcher := NewWordMatcher(words, true) // case insensitive
	defer matcher.Close()

	return matcher.FileContainsWords(filePath, words), nil
}

// CheckFileContainsExcludeWords checks if a file contains any exclude words
func CheckFileContainsExcludeWords(filePath string, excludeWords []string) (bool, error) {
	if len(excludeWords) == 0 {
		return false, nil
	}

	matcher := NewWordMatcher(excludeWords, true) // case insensitive
	defer matcher.Close()

	// Returns true if ANY exclude word is found
	for _, word := range excludeWords {
		if matcher.FileContainsWords(filePath, []string{word}) {
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

	fileSize := stat.Size()

	// Use matcher to read content efficiently
	matcher := NewWordMatcher([]string{}, true)
	defer matcher.Close()

	// For large files, we'll read a portion for excerpt extraction
	maxReadSize := int64(10 * 1024 * 1024) // 10MB max for content extraction
	if fileSize > maxReadSize {
		// Read first portion of large files
		buffer := make([]byte, maxReadSize)
		n, err := file.Read(buffer)
		if err != nil && n == 0 {
			return "", fileSize, err
		}
		return string(buffer[:n]), fileSize, nil
	}

	// Read entire smaller file
	buffer := make([]byte, fileSize)
	n, err := file.Read(buffer)
	if err != nil && n == 0 {
		return "", fileSize, err
	}

	return string(buffer[:n]), fileSize, nil
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

// getFileInfo is a helper function to get file information
func getFileInfo(filePath string) (os.FileInfo, error) {
	return os.Stat(filePath)
}
