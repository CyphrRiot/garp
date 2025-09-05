package config

// DocumentTypes defines the file extensions for document files
var DocumentTypes = []string{
	"txt", "md", "html", "xml", "csv", "yaml", "yml",
	"eml", "mbox", "msg", "pdf",
	"doc", "docx", "xls", "xlsx", "ppt", "pptx",
	"odt", "ods", "odp", "rtf",
	"log", "cfg", "conf", "ini", "sh", "bat",
}

// CodeTypes defines the file extensions for programming files
var CodeTypes = []string{
	"js", "ts", "sql", "py", "php", "java", "cpp", "c", "json",
	"go", "rs", "rb", "cs", "swift", "kt", "scala", "clj",
	"h", "hpp", "cc", "cxx", "pl", "r", "m", "mm",
}

// BuildRipgrepFileTypes creates ripgrep file type arguments
func BuildRipgrepFileTypes(includeCode bool) []string {
	types := make([]string, 0)
	
	// Add document types
	for _, ext := range DocumentTypes {
		types = append(types, "-t", ext)
	}
	
	// Add code types if requested
	if includeCode {
		for _, ext := range CodeTypes {
			types = append(types, "-t", ext)
		}
	}
	
	return types
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
