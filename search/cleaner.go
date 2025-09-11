package search

import (
	"fmt"
	"regexp"
	"sort"
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
	whitespaceRegex  = regexp.MustCompile(`\s+`)

	// Lines with too many special characters (likely markup remnants)
	junkLineRegex = regexp.MustCompile(`^[^a-zA-Z]*$|^[{}[\]();:=<>|\\]{3,}`)

	// Junk lines with excessive = or #
	junkSymbolsRegex = regexp.MustCompile(`(?m)^[\=\#]{5,}$`)
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

	// Remove junk lines with excessive = or #
	content = junkSymbolsRegex.ReplaceAllString(content, "")

	// Normalize whitespace
	content = whitespaceRegex.ReplaceAllString(content, " ")

	return strings.TrimSpace(content)
}

// ExtractMeaningfulExcerpts extracts clean, readable excerpts around search terms
func ExtractMeaningfulExcerpts(content string, searchTerms []string, maxExcerpts int) []string {
	// Clean content first
	cleaned := CleanContent(content)

	// Find all positions of all words
	type wordMatch struct {
		pos       int
		wordIndex int
	}
	var allMatches []wordMatch
	for i, term := range searchTerms {
		pattern := fmt.Sprintf(`(?i)\b%s\b`, regexp.QuoteMeta(term))
		regex := regexp.MustCompile(pattern)
		indexes := regex.FindAllStringIndex(cleaned, -1)
		for _, idx := range indexes {
			allMatches = append(allMatches, wordMatch{pos: idx[0], wordIndex: i})
		}
	}

	if len(allMatches) < len(searchTerms) {
		return []string{}
	}

	// Sort by position
	sort.Slice(allMatches, func(i, j int) bool {
		return allMatches[i].pos < allMatches[j].pos
	})

	// Find minimal window containing all words
	minWindow := len(cleaned)
	windowStart := -1
	wordCount := make(map[int]int)
	requiredWords := len(searchTerms)
	currentWords := 0
	left := 0

	for right := 0; right < len(allMatches); right++ {
		wordCount[allMatches[right].wordIndex]++
		if wordCount[allMatches[right].wordIndex] == 1 {
			currentWords++
		}

		for currentWords == requiredWords && left <= right {
			windowSize := allMatches[right].pos - allMatches[left].pos
			if windowSize < minWindow {
				minWindow = windowSize
				windowStart = left
			}
			wordCount[allMatches[left].wordIndex]--
			if wordCount[allMatches[left].wordIndex] == 0 {
				currentWords--
			}
			left++
		}
	}

	if windowStart == -1 {
		return []string{}
	}

	// Extract the entire minimal window with padding
	startPos := allMatches[windowStart].pos
	endPos := allMatches[windowStart+requiredWords-1].pos + len(searchTerms[allMatches[windowStart+requiredWords-1].wordIndex])

	// Extract from start with padding to end with padding
	extractStart := max(0, startPos-50)
	extractEnd := min(len(cleaned), endPos+50)

	// Find word boundaries
	for extractStart > 0 && cleaned[extractStart] != ' ' && cleaned[extractStart] != '\n' {
		extractStart--
	}
	for extractEnd < len(cleaned) && cleaned[extractEnd] != ' ' && cleaned[extractEnd] != '\n' {
		extractEnd++
	}

	excerpt := strings.TrimSpace(cleaned[extractStart:extractEnd])

	// Clean up
	excerpt = strings.ReplaceAll(excerpt, "\n", " ")
	excerpt = strings.ReplaceAll(excerpt, "\t", " ")
	excerpt = regexp.MustCompile(`\s+`).ReplaceAllString(excerpt, " ")

	if len(excerpt) >= 20 && hasLetters(excerpt) {
		highlighted := HighlightTerms(excerpt, searchTerms)
		return []string{highlighted}
	}

	// Fallback: if precise excerpt extraction fails, grab a small window around
	// the first occurrence of any search term so we still show useful context.
	for _, term := range searchTerms {
		if strings.TrimSpace(term) == "" {
			continue
		}
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(term) + `\b`)
		loc := re.FindStringIndex(cleaned)
		if loc != nil {
			fallbackStart := max(0, loc[0]-80)
			fallbackEnd := min(len(cleaned), loc[1]+80)

			// Expand to word boundaries
			for fallbackStart > 0 && cleaned[fallbackStart] != ' ' && cleaned[fallbackStart] != '\n' {
				fallbackStart--
			}
			for fallbackEnd < len(cleaned) && cleaned[fallbackEnd] != ' ' && cleaned[fallbackEnd] != '\n' {
				fallbackEnd++
			}

			fallback := strings.TrimSpace(cleaned[fallbackStart:fallbackEnd])
			fallback = strings.ReplaceAll(fallback, "\n", " ")
			fallback = strings.ReplaceAll(fallback, "\t", " ")
			fallback = regexp.MustCompile(`\s+`).ReplaceAllString(fallback, " ")
			if len(fallback) > 0 && hasLetters(fallback) {
				return []string{HighlightTerms(fallback, searchTerms)}
			}
		}
	}

	return []string{}
}

// matchInfo represents a search term match location
type matchInfo struct {
	start int
	end   int
	term  string
}

// containsAnySearchTerm checks if text contains any of the search terms
func containsAnySearchTerm(text string, searchTerms []string) bool {
	for _, term := range searchTerms {
		if containsWholeWord(text, term) {
			return true
		}
	}
	return false
}

// splitIntoSentences splits content into sentences for better excerpt extraction
func splitIntoSentences(content string) []string {
	// Clean content first
	cleaned := CleanContent(content)

	// Split by common sentence endings
	sentences := regexp.MustCompile(`[.!?]+\s+`).Split(cleaned, -1)

	// If no sentence breaks, split by line breaks
	if len(sentences) == 1 {
		sentences = strings.Split(cleaned, "\n")
	}

	// If still one big chunk, split by double spaces or long phrases
	if len(sentences) == 1 && len(cleaned) > 500 {
		// Try splitting by double spaces or other natural breaks
		if strings.Contains(cleaned, "  ") {
			sentences = strings.Split(cleaned, "  ")
		} else {
			// Split into chunks of ~100 characters at word boundaries
			sentences = chunkText(cleaned, 100)
		}
	}

	return sentences
}

// chunkText splits text into chunks at word boundaries
func chunkText(text string, chunkSize int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}

	var chunks []string
	var currentChunk strings.Builder

	for _, word := range words {
		if currentChunk.Len()+len(word)+1 > chunkSize && currentChunk.Len() > 0 {
			chunks = append(chunks, currentChunk.String())
			currentChunk.Reset()
		}

		if currentChunk.Len() > 0 {
			currentChunk.WriteString(" ")
		}
		currentChunk.WriteString(word)
	}

	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}

	return chunks
}

// isObviousJunk determines if a line is obviously just markup/noise - less strict than isJunkLine
func isObviousJunk(line string) bool {
	// Skip email headers
	if emailHeaderRegex.MatchString(line) {
		return true
	}

	// Skip lines that are ONLY special characters
	if junkLineRegex.MatchString(line) {
		return true
	}

	// If line has at least some letters, keep it
	letterCount := 0
	for _, r := range line {
		if unicode.IsLetter(r) {
			letterCount++
		}
	}

	// Only reject if there are no letters at all
	return letterCount == 0
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
	const HI = "\033[1;31m" // bold red for stronger, more visible highlighting
	const NC = "\033[0m"

	result := text
	for _, term := range searchTerms {
		pattern := fmt.Sprintf(`\b%s\b`, regexp.QuoteMeta(term))
		regex := regexp.MustCompile(`(?i)` + pattern)
		result = regex.ReplaceAllStringFunc(result, func(match string) string {
			return HI + match + NC
		})
	}

	return result
}

// hasLetters checks if a string contains any letters
func hasLetters(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
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
