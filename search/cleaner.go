package search

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	// HTML/XML tags
	htmlTagRegex = regexp.MustCompile(`<[^>]*>`)
	
	// HTML entities
	htmlEntityRegex = regexp.MustCompile(`&[a-zA-Z0-9#]*;`)
	
	// Email headers (case insensitive)
	emailHeaderRegex = regexp.MustCompile(`(?i)^(Content-Type|Content-Transfer-Encoding|MIME-Version|Date|From|To|Subject|Message-ID|Return-Path|Received|X-[^:]*|Authentication-Results):`)
	
	// CSS/JavaScript blocks (separate patterns since Go doesn't support backreferences)
	cssRegex = regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`)
	jsRegex  = regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
	
	// Control characters and excessive whitespace
	controlCharRegex = regexp.MustCompile(`[\x00-\x1f\x7f-\x9f]`)
	whitespaceRegex = regexp.MustCompile(`\s+`)
	
	// Lines with too many special characters (likely markup remnants)
	junkLineRegex = regexp.MustCompile(`^[^a-zA-Z]*$|^[{}[\]();:=<>|\\]{3,}`)
)

// CleanContent removes markup, headers, and other noise from content
func CleanContent(content string) string {
	// Remove CSS and JavaScript blocks first
	content = cssRegex.ReplaceAllString(content, "")
	content = jsRegex.ReplaceAllString(content, "")
	
	// Remove HTML tags
	content = htmlTagRegex.ReplaceAllString(content, " ")
	
	// Remove HTML entities
	content = htmlEntityRegex.ReplaceAllString(content, " ")
	
	// Remove control characters
	content = controlCharRegex.ReplaceAllString(content, "")
	
	// Normalize whitespace
	content = whitespaceRegex.ReplaceAllString(content, " ")
	
	return strings.TrimSpace(content)
}

// ExtractMeaningfulExcerpts extracts clean, readable excerpts around search terms
func ExtractMeaningfulExcerpts(content string, searchTerms []string, maxExcerpts int) []string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	lines := make([]string, 0)
	
	// Read all lines
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
	}
	
	excerpts := make([]string, 0, maxExcerpts)
	used := make(map[int]bool) // Track which lines we've already used
	
	// Find lines containing search terms
	for i, line := range lines {
		if used[i] || len(excerpts) >= maxExcerpts {
			continue
		}
		
		// Check if line contains any search term
		hasSearchTerm := false
		cleanLine := CleanContent(line)
		
		// Skip if line is too short or looks like junk
		if len(cleanLine) < 15 || isJunkLine(cleanLine) {
			continue
		}
		
		// Check for search terms with word boundaries
		for _, term := range searchTerms {
			if containsWholeWord(cleanLine, term) {
				hasSearchTerm = true
				break
			}
		}
		
		if hasSearchTerm {
			// Extract context (2 lines before and after)
			start := max(0, i-2)
			end := min(len(lines), i+3)
			
			contextLines := make([]string, 0)
			for j := start; j < end; j++ {
				if !used[j] {
					contextLine := CleanContent(lines[j])
					if len(contextLine) >= 10 && !isJunkLine(contextLine) {
						contextLines = append(contextLines, contextLine)
						used[j] = true
					}
				}
			}
			
			if len(contextLines) > 0 {
				excerpt := strings.Join(contextLines, " ")
				if len(excerpt) > 30 { // Ensure meaningful content
					excerpts = append(excerpts, excerpt)
				}
			}
		}
	}
	
	return excerpts
}

// isJunkLine determines if a line is likely noise/markup
func isJunkLine(line string) bool {
	// Skip email headers
	if emailHeaderRegex.MatchString(line) {
		return true
	}
	
	// Skip lines that are mostly special characters
	if junkLineRegex.MatchString(line) {
		return true
	}
	
	// Count special characters vs letters
	specialCount := 0
	letterCount := 0
	
	for _, r := range line {
		if unicode.IsLetter(r) {
			letterCount++
		} else if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			specialCount++
		}
	}
	
	// If more than 60% special characters, consider it junk
	if letterCount > 0 && float64(specialCount)/float64(letterCount) > 0.6 {
		return true
	}
	
	return false
}

// containsWholeWord checks if text contains a whole word (case insensitive)
func containsWholeWord(text, word string) bool {
	pattern := fmt.Sprintf(`\b%s\b`, regexp.QuoteMeta(word))
	regex := regexp.MustCompile(`(?i)` + pattern)
	return regex.MatchString(text)
}

// HighlightTerms highlights search terms in text with color codes
func HighlightTerms(text string, searchTerms []string) string {
	const RED = "\033[0;31m"
	const NC = "\033[0m"
	
	result := text
	for _, term := range searchTerms {
		pattern := fmt.Sprintf(`\b%s\b`, regexp.QuoteMeta(term))
		regex := regexp.MustCompile(`(?i)` + pattern)
		result = regex.ReplaceAllStringFunc(result, func(match string) string {
			return RED + match + NC
		})
	}
	
	return result
}

// Helper functions
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
