package config

import (
	"slices"
	"strings"
)

// DocumentTypes defines the file extensions for document files
var DocumentTypes = []string{
	"txt", "md", "html", "xml", "csv", "yaml", "yml",
	"eml", "mbox", "msg",
	"pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx",
	"odt", "ods", "odp", "rtf",
	"log", "cfg", "conf", "ini", "sh", "bat",
}

// CodeTypes defines the file extensions for programming files
var CodeTypes = []string{
	"js", "ts", "sql", "py", "php", "java", "cpp", "c", "json",
	"go", "rs", "rb", "cs", "swift", "kt", "scala", "clj",
	"h", "hpp", "cc", "cxx", "pl", "r", "m", "mm",
}

// IsDocumentFile checks if a file extension is a document type
func IsDocumentFile(filename string) bool {
	ext := strings.ToLower(strings.TrimPrefix(getFileExtension(filename), "."))
	if slices.Contains(DocumentTypes, ext) {
		return true
	}
	return false
}

// IsCodeFile checks if a file extension is a code type
func IsCodeFile(filename string) bool {
	ext := strings.ToLower(strings.TrimPrefix(getFileExtension(filename), "."))
	if slices.Contains(CodeTypes, ext) {
		return true
	}
	return false
}

// GetAllSupportedTypes returns all supported file types based on includeCode flag
func GetAllSupportedTypes(includeCode bool) []string {
	types := make([]string, len(DocumentTypes))
	copy(types, DocumentTypes)

	if includeCode {
		types = append(types, CodeTypes...)
	}

	return types
}

// BuildFileTypeMap creates a map for O(1) file type lookups
func BuildFileTypeMap(includeCode bool) map[string]bool {
	typeMap := make(map[string]bool)

	// Add document types
	for _, ext := range DocumentTypes {
		typeMap["."+ext] = true
	}

	// Add code types if requested
	if includeCode {
		for _, ext := range CodeTypes {
			typeMap["."+ext] = true
		}
	}

	return typeMap
}

// GetEstimatedSearchTime returns time estimate based on file count
func GetEstimatedSearchTime(fileCount int) string {
	switch {
	case fileCount < 100:
		return "under 10 seconds"
	case fileCount < 1000:
		return "10-30 seconds"
	case fileCount < 5000:
		return "30 seconds - 2 minutes"
	default:
		return "2-10 minutes (depends on file sizes)"
	}
}

// GetPerformanceProfile returns optimal settings based on file count
func GetPerformanceProfile(fileCount int) (workers int, bufferSize int) {
	switch {
	case fileCount < 100:
		return 2, 50 // Light workload
	case fileCount < 1000:
		return 4, 200 // Medium workload
	case fileCount < 10000:
		return 8, 500 // Heavy workload
	default:
		return 16, 1000 // Very heavy workload
	}
}

// getFileExtension extracts file extension from filename
func getFileExtension(filename string) string {
	lastDot := strings.LastIndex(filename, ".")
	if lastDot == -1 || lastDot == len(filename)-1 {
		return ""
	}
	return filename[lastDot:]
}

// IsHiddenFile checks if a file should be treated as hidden
func IsHiddenFile(filename string) bool {
	return strings.HasPrefix(filename, ".")
}

// ShouldSkipDirectory determines if a directory should be skipped during traversal
func ShouldSkipDirectory(dirName string) bool {
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
		"tmp":           true,
		"temp":          true,
		".DS_Store":     true,
	}

	return skipDirs[dirName] || strings.HasPrefix(dirName, ".")
}

// GetFileTypeDescription returns a human-readable description of file types
func GetFileTypeDescription(includeCode bool) string {
	if includeCode {
		return "documents (txt, md, html, xml, csv, yaml, yml, eml, mbox, msg, pdf, doc, docx, odt, rtf, log, cfg, conf, ini, sh, bat) + code files (go, js, ts, py, php, java, cpp, c, json, rs, rb, cs, swift, kt, scala)"
	}
	return "documents (txt, md, html, xml, csv, yaml, yml, eml, mbox, msg, pdf, doc, docx, odt, rtf, log, cfg, conf, ini, sh, bat)"
}

// BuildRipgrepFileTypes creates ripgrep file type arguments
func BuildRipgrepFileTypes(includeCode bool) []string {
	// Use glob patterns for ALL document types
	types := []string{
		// Text files
		"-g", "*.txt", "-g", "*.md", "-g", "*.log", "-g", "*.rtf",
		// HTML/Web files - all variants
		"-g", "*.html", "-g", "*.htm", "-g", "*.xhtml", "-g", "*.shtml",
		// XML/Data files
		"-g", "*.xml", "-g", "*.csv", "-g", "*.yaml", "-g", "*.yml",
		// Config files
		"-g", "*.cfg", "-g", "*.conf", "-g", "*.ini",
		// Email files
		"-g", "*.eml", "-g", "*.mbox", "-g", "*.msg",
		// Office documents
		"-g", "*.pdf", "-g", "*.doc", "-g", "*.docx",
		"-g", "*.odt",
		// Scripts
		"-g", "*.sh", "-g", "*.bat", "-g", "*.cmd",
		// Other text formats
		"-g", "*.tex", "-g", "*.rst", "-g", "*.asciidoc",
	}

	// Add code file patterns if requested
	if includeCode {
		codeGlobs := []string{
			"-g", "*.js", "-g", "*.ts", "-g", "*.sql", "-g", "*.py",
			"-g", "*.php", "-g", "*.java", "-g", "*.cpp", "-g", "*.c",
			"-g", "*.json", "-g", "*.go", "-g", "*.rs", "-g", "*.rb",
			"-g", "*.cs", "-g", "*.swift", "-g", "*.kt", "-g", "*.scala",
		}
		types = append(types, codeGlobs...)
	}

	return types
}

// EstimateMemoryUsage provides memory usage estimate based on file count
func EstimateMemoryUsage(fileCount int) string {
	switch {
	case fileCount < 1000:
		return "~50-100 MB"
	case fileCount < 10000:
		return "~100-500 MB"
	case fileCount < 50000:
		return "~500MB-1GB"
	default:
		return "~1-2GB (large dataset)"
	}
}
