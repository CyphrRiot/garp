package search

import (
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// FileInfo represents information about a file
type FileInfo struct {
	Path string
	Size int64
}

// GetDocumentFileCount returns the count of document files that will be searched
func GetDocumentFileCount(fileTypes []string) (int, error) {
	args := []string{"-l", "--files", "--no-ignore", "--hidden"}
	args = append(args, fileTypes...)
	
	cmd := exec.Command("rg", args...)
	output, err := cmd.Output()
	if err != nil {
		return 0, nil // No files found is not an error
	}
	
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0, nil
	}
	
	return len(lines), nil
}

// FindFilesWithFirstWord finds all files containing the first search word
func FindFilesWithFirstWord(word string, fileTypes []string) ([]string, error) {
	pattern := `\b` + word + `\b`
	
	args := []string{"-i", "-l", "--no-ignore", "--hidden"}
	args = append(args, fileTypes...)
	args = append(args, pattern)
	
	cmd := exec.Command("rg", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, nil // No files found is not an error
	}
	
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	
	return lines, nil
}

// CheckFileContainsAllWords checks if a file contains all search words
func CheckFileContainsAllWords(filePath string, words []string) (bool, error) {
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
	
	// Check each word
	for _, word := range words {
		if !containsWholeWord(contentStr, strings.ToLower(word)) {
			return false, nil
		}
	}
	
	return true, nil
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
