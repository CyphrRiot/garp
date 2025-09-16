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

	// Email quoting lines and markers
	emailQuotingRegex   = regexp.MustCompile(`(?m)^>+.*$`)
	quoteLineStartRegex = regexp.MustCompile(`(?m)^\s*>+\s*`)
	quoteMidRegex       = regexp.MustCompile(`\s*>+\s*`)
)

var excerptContextLimit int // 0 means auto (use default heuristic)

// SetExcerptContextLimit allows the engine to set an excerpt window hint (usually the distance).
// Use 0 to reset to automatic behavior.
func SetExcerptContextLimit(n int) {
	if n < 0 {
		n = 0
	}
	excerptContextLimit = n
}

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

	// Remove email quoting lines
	content = emailQuotingRegex.ReplaceAllString(content, "")
	// Strip leading '>' quote markers at line starts
	content = quoteLineStartRegex.ReplaceAllString(content, "")
	// Collapse midline '>' quote markers (e.g., >>>> >>>>) into a single space
	content = quoteMidRegex.ReplaceAllString(content, " ")

	// Normalize whitespace
	content = whitespaceRegex.ReplaceAllString(content, " ")

	// Insert missing spaces to improve readability in extracted content
	// Letter followed by digit (e.g., "Account10" -> "Account 10")
	content = regexp.MustCompile(`([A-Za-z])([0-9])`).ReplaceAllString(content, `$1 $2`)
	// Digit followed by letter (e.g., "7367NEXT" -> "7367 NEXT")
	content = regexp.MustCompile(`([0-9])([A-Za-z])`).ReplaceAllString(content, `$1 $2`)
	// Ensure a space after commas when missing (e.g., "21,5:43" -> "21, 5:43")
	content = regexp.MustCompile(`,([^\s])`).ReplaceAllString(content, `, $1`)

	return strings.TrimSpace(content)
}

// ExtractMeaningfulExcerpts returns targeted, per-match snippets around each term.
// We extract tight, local windows around each match with email-aware boundaries,
// paragraph fallbacks, and punctuation-aware sentence ends. We avoid global scans.
func ExtractMeaningfulExcerpts(content string, searchTerms []string, maxExcerpts int) []string {
	// Line-preserving clean for boundary finding: remove heavy markup/noise but keep newlines
	prep := cssRegex.ReplaceAllString(content, "")
	prep = jsRegex.ReplaceAllString(prep, "")
	prep = htmlTagRegex.ReplaceAllString(prep, " ")
	prep = htmlEntityRegex.ReplaceAllString(prep, " ")
	prep = controlCharRegex.ReplaceAllString(prep, "")
	prep = junkSymbolsRegex.ReplaceAllString(prep, "")
	prep = emailQuotingRegex.ReplaceAllString(prep, "")
	// Strip leading '>' markers and collapse midline quote markers
	prep = quoteLineStartRegex.ReplaceAllString(prep, "")
	prep = quoteMidRegex.ReplaceAllString(prep, " ")
	cleaned := prep

	if maxExcerpts <= 0 {
		maxExcerpts = 3
	}
	if len(cleaned) == 0 || len(searchTerms) == 0 {
		return []string{}
	}

	// Build regexes for each term (whole-word, case-insensitive)
	termRE := make([]*regexp.Regexp, 0, len(searchTerms))
	for _, t := range searchTerms {
		tt := strings.TrimSpace(t)
		if tt == "" {
			continue
		}
		termRE = append(termRE, regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(tt)+`\b`))
	}
	if len(termRE) == 0 {
		return []string{}
	}

	// Clamp window for scanning sentence boundaries around each match
	maxContext := func() int {
		// Base from configured excerptContextLimit (typically the search distance). 0 means auto default.
		base := excerptContextLimit
		if base <= 0 {
			base = 800
		}
		// Scale relative to distance but clamp to keep excerpts readable and performant.
		win := base * 2
		if win < 200 {
			win = 200
		}
		if win > 4000 {
			win = 4000
		}
		return win
	}()
	excerpts := make([]string, 0, maxExcerpts)
	seen := make(map[string]struct{})

	// Sliding-window strategy (early): pick smallest span covering all terms and prefer that excerpt first.
	if len(termRE) > 1 {
		type tmatch struct {
			pos int
			idx int
		}
		all := make([]tmatch, 0, 128)
		for i, re := range termRE {
			idxs := re.FindAllStringIndex(cleaned, -1)
			for _, loc := range idxs {
				all = append(all, tmatch{pos: loc[0], idx: i})
			}
		}
		if len(all) > 0 {
			sort.Slice(all, func(i, j int) bool { return all[i].pos < all[j].pos })
			counts := make(map[int]int, len(termRE))
			covered := 0
			l := 0
			bestL, bestR := -1, -1
			for r := 0; r < len(all); r++ {
				mm := all[r]
				if counts[mm.idx] == 0 {
					covered++
				}
				counts[mm.idx]++
				for covered == len(termRE) {
					curL := all[l].pos
					curR := all[r].pos
					if bestL == -1 || (curR-curL) < (bestR-bestL) {
						bestL, bestR = curL, curR
					}
					leftm := all[l]
					counts[leftm.idx]--
					if counts[leftm.idx] == 0 {
						covered--
					}
					l++
				}
			}
			if bestL >= 0 && len(excerpts) < maxExcerpts {
				left := bestL
				right := bestR
				limitLeft := left - maxContext/2
				if limitLeft < 0 {
					limitLeft = 0
				}
				// Expand left to sentence/email-header boundary
				for left > limitLeft {
					if cleaned[left] == '.' || cleaned[left] == '!' || cleaned[left] == '?' {
						break
					}
					if cleaned[left] == '\n' {
						ls := left + 1
						le := ls
						for le < len(cleaned) && cleaned[le] != '\n' && le-ls < 128 {
							le++
						}
						if ls < len(cleaned) && emailHeaderRegex.MatchString(cleaned[ls:le]) {
							break
						}
					}
					left--
				}
				if left > 0 && (cleaned[left] == '.' || cleaned[left] == '!' || cleaned[left] == '?') {
					left++
				} else if left <= limitLeft {
					// Paragraph fallback
					if idx := strings.LastIndex(cleaned[limitLeft:bestL], "\n\n"); idx != -1 {
						left = limitLeft + idx + 2
					} else if idx := strings.LastIndex(cleaned[limitLeft:bestL], "\n"); idx != -1 {
						left = limitLeft + idx + 1
					} else {
						left = limitLeft
					}
				}

				limitRight := right + maxContext/2
				if limitRight > len(cleaned) {
					limitRight = len(cleaned)
				}
				// Expand right to sentence/email-header boundary
				for right < limitRight {
					if cleaned[right] == '.' || cleaned[right] == '!' || cleaned[right] == '?' {
						right++
						break
					}
					if cleaned[right] == '\n' {
						ls := right + 1
						le := ls
						for le < len(cleaned) && cleaned[le] != '\n' && le-ls < 128 {
							le++
						}
						if ls < len(cleaned) && emailHeaderRegex.MatchString(cleaned[ls:le]) {
							break
						}
					}
					right++
				}
				if right >= limitRight {
					// Paragraph fallback
					if idx := strings.Index(cleaned[bestR:limitRight], "\n\n"); idx != -1 {
						right = bestR + idx
					} else if idx := strings.Index(cleaned[bestR:limitRight], "\n"); idx != -1 {
						right = bestR + idx
					} else {
						right = limitRight
					}
				}

				// Build stitched, sentence-aware excerpt and dedupe
				snippet := strings.TrimSpace(cleaned[left:right])
				if snippet != "" {
					// Split the snippet into sentences and keep only those containing any search term
					sentences := splitIntoSentences(snippet)
					parts := make([]string, 0, len(sentences))
					for _, s := range sentences {
						st := strings.TrimSpace(s)
						if st == "" {
							continue
						}
						if containsAnySearchTerm(st, searchTerms) {
							parts = append(parts, st)
						}
					}
					var ex string
					if len(parts) > 0 {
						// Join with ellipses for clarity between separate sentences
						ex = strings.Join(parts, " … ")
					} else {
						// Fallback to the raw snippet window if no sentence matched the filter
						ex = snippet
					}
					// Normalize whitespace for display
					ex = strings.ReplaceAll(ex, "\n", " ")
					ex = strings.ReplaceAll(ex, "\t", " ")
					ex = regexp.MustCompile(`\s+`).ReplaceAllString(ex, " ")
					if _, ok := seen[ex]; !ok {
						seen[ex] = struct{}{}
						excerpts = append(excerpts, ex)
						// Prefer the all-terms excerpt first; return early if we have it
						return excerpts
					}
				}
			}
		}
	}

	// Ensure at least one sentence per term (when possible)
	for _, re := range termRE {
		if len(excerpts) >= maxExcerpts {
			break
		}
		locs := re.FindAllStringIndex(cleaned, 3) // up to 3 matches per term
		for _, loc := range locs {
			if len(excerpts) >= maxExcerpts {
				break
			}
			start := loc[0]
			end := loc[1]

			// Find local sentence boundaries with clamped scan (email-aware + punctuation)
			left := start
			limitLeft := left - maxContext/2
			if limitLeft < 0 {
				limitLeft = 0
			}
			// Expand left until punctuation or email header boundary
			for left > limitLeft {
				if cleaned[left] == '.' || cleaned[left] == '!' || cleaned[left] == '?' {
					break
				}
				// Stop at email header lines (From:, To:, Subject:, Date:, etc.)
				if cleaned[left] == '\n' {
					ls := left + 1
					le := ls
					for le < len(cleaned) && cleaned[le] != '\n' && le-ls < 128 {
						le++
					}
					if ls < len(cleaned) && emailHeaderRegex.MatchString(cleaned[ls:le]) {
						break
					}
				}
				left--
			}
			if left > 0 && (cleaned[left] == '.' || cleaned[left] == '!' || cleaned[left] == '?') {
				left++
			} else if left <= limitLeft {
				// Paragraph fallback: last blank line in window
				if idx := strings.LastIndex(cleaned[limitLeft:start], "\n\n"); idx != -1 {
					left = limitLeft + idx + 2
				} else {
					// Single newline fallback
					if idx := strings.LastIndex(cleaned[limitLeft:start], "\n"); idx != -1 {
						left = limitLeft + idx + 1
					} else {
						left = limitLeft
					}
				}
			}

			right := end
			limitRight := right + maxContext/2
			if limitRight > len(cleaned) {
				limitRight = len(cleaned)
			}
			// Expand right until punctuation or email header boundary
			for right < limitRight {
				if cleaned[right] == '.' || cleaned[right] == '!' || cleaned[right] == '?' {
					right++
					break
				}
				// Stop at email header lines (From:, To:, Subject:, Date:, etc.)
				if cleaned[right] == '\n' {
					ls := right + 1
					le := ls
					for le < len(cleaned) && cleaned[le] != '\n' && le-ls < 128 {
						le++
					}
					if ls < len(cleaned) && emailHeaderRegex.MatchString(cleaned[ls:le]) {
						break
					}
				}
				right++
			}
			if right >= limitRight {
				// Paragraph fallback: next blank line in window
				if idx := strings.Index(cleaned[end:limitRight], "\n\n"); idx != -1 {
					right = end + idx
				} else if idx := strings.Index(cleaned[end:limitRight], "\n"); idx != -1 {
					right = end + idx
				} else {
					right = limitRight
				}
			}

			// Build sentence snippet and normalize whitespace
			ex := strings.TrimSpace(cleaned[left:right])
			if ex == "" {
				continue
			}
			ex = strings.ReplaceAll(ex, "\n", " ")
			ex = strings.ReplaceAll(ex, "\t", " ")
			ex = regexp.MustCompile(`\s+`).ReplaceAllString(ex, " ")

			if _, ok := seen[ex]; ok {
				continue
			}
			seen[ex] = struct{}{}
			excerpts = append(excerpts, ex)
		}
	}

	if len(excerpts) > 0 {
		return excerpts
	}

	// Fallback: find the first occurrence of any term and expand within a small window
	for _, re := range termRE {
		loc := re.FindStringIndex(cleaned)
		if loc == nil {
			continue
		}
		start := loc[0]
		end := loc[1]

		left := start
		limitLeft := left - maxContext/2
		if limitLeft < 0 {
			limitLeft = 0
		}
		for left > limitLeft {
			if cleaned[left] == '.' || cleaned[left] == '!' || cleaned[left] == '?' {
				break
			}
			if cleaned[left] == '\n' {
				ls := left + 1
				le := ls
				for le < len(cleaned) && cleaned[le] != '\n' && le-ls < 128 {
					le++
				}
				if ls < len(cleaned) && emailHeaderRegex.MatchString(cleaned[ls:le]) {
					break
				}
			}
			left--
		}
		if left > 0 && (cleaned[left] == '.' || cleaned[left] == '!' || cleaned[left] == '?') {
			left++
		} else if left <= limitLeft {
			if idx := strings.LastIndex(cleaned[limitLeft:start], "\n\n"); idx != -1 {
				left = limitLeft + idx + 2
			} else if idx := strings.LastIndex(cleaned[limitLeft:start], "\n"); idx != -1 {
				left = limitLeft + idx + 1
			} else {
				left = limitLeft
			}
		}

		right := end
		limitRight := right + maxContext/2
		if limitRight > len(cleaned) {
			limitRight = len(cleaned)
		}
		for right < limitRight {
			if cleaned[right] == '.' || cleaned[right] == '!' || cleaned[right] == '?' {
				right++
				break
			}
			if cleaned[right] == '\n' {
				ls := right + 1
				le := ls
				for le < len(cleaned) && cleaned[le] != '\n' && le-ls < 128 {
					le++
				}
				if ls < len(cleaned) && emailHeaderRegex.MatchString(cleaned[ls:le]) {
					break
				}
			}
			right++
		}
		if right >= limitRight {
			if idx := strings.Index(cleaned[end:limitRight], "\n\n"); idx != -1 {
				right = end + idx
			} else if idx := strings.Index(cleaned[end:limitRight], "\n"); idx != -1 {
				right = end + idx
			} else {
				right = limitRight
			}
		}

		ex := strings.TrimSpace(cleaned[left:right])
		ex = strings.ReplaceAll(ex, "\n", " ")
		ex = strings.ReplaceAll(ex, "\t", " ")
		ex = regexp.MustCompile(`\s+`).ReplaceAllString(ex, " ")
		if ex != "" {
			return []string{ex}
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
// Improved boundaries:
// - End at punctuation followed by whitespace OR directly followed by an uppercase letter.
// - Avoid splitting inside common abbreviations (e.g., "e.g.", "i.e.", "Dr.", "U.S.") and decimals (e.g., "3.14").
// - Treat double newlines as hard paragraph boundaries.
// Falls back to line or chunk splitting when boundaries are scarce.
func splitIntoSentences(content string) []string {
	// Clean content first
	cleaned := CleanContent(content)
	if strings.TrimSpace(cleaned) == "" {
		return []string{}
	}

	// Common abbreviations (lowercased without trailing dot)
	abbr := map[string]bool{
		"mr": true, "mrs": true, "ms": true, "dr": true, "prof": true,
		"sr": true, "jr": true, "st": true, "vs": true, "no": true,
		"inc": true, "ltd": true, "co": true, "u.s": true, "u.k": true,
		"e.g": true, "i.e": true,
	}

	sentences := make([]string, 0, 16)
	var sb strings.Builder

	isUpper := func(r rune) bool { return unicode.IsUpper(r) }
	isDigit := func(r rune) bool { return r >= '0' && r <= '9' }

	runes := []rune(cleaned)
	n := len(runes)
	for i := 0; i < n; i++ {
		r := runes[i]
		sb.WriteRune(r)

		// Hard paragraph boundary on double newline
		if r == '\n' {
			if i+1 < n && runes[i+1] == '\n' {
				s := strings.TrimSpace(sb.String())
				if s != "" {
					sentences = append(sentences, s)
				}
				sb.Reset()
				i++ // consume the second newline
				continue
			}
		}

		// Sentence-ending punctuation
		if r == '.' || r == '!' || r == '?' {
			// Avoid decimals: digit '.' digit
			if i > 0 && i+1 < n && isDigit(runes[i-1]) && isDigit(runes[i+1]) {
				continue
			}

			// Abbreviation check: capture token before dot
			j := i - 1
			for j >= 0 && unicode.IsSpace(runes[j]) {
				j--
			}
			k := j
			for k >= 0 && unicode.IsLetter(runes[k]) {
				k--
			}
			token := strings.ToLower(string(runes[k+1 : j+1])) // letters immediately before '.'
			if token != "" && abbr[token] {
				// e.g., "Dr.", "Inc.", "e.g.", "U.S."
				// For initialisms like "U.S." the next '.' will be handled here as well.
				continue
			}

			// Decide boundary based on next rune
			nextIsUpper, nextIsSpace := false, false
			if i+1 < n {
				next := runes[i+1]
				nextIsUpper = isUpper(next)
				nextIsSpace = unicode.IsSpace(next)
			} else {
				nextIsSpace = true
			}

			if nextIsSpace || nextIsUpper {
				// End of sentence here
				s := strings.TrimSpace(sb.String())
				if s != "" {
					sentences = append(sentences, s)
				}
				sb.Reset()

				// Skip a single space after punctuation to avoid leading space
				if nextIsSpace && i+1 < n && unicode.IsSpace(runes[i+1]) {
					i++
				}
			}
		}
	}

	// Flush remainder
	if sb.Len() > 0 {
		s := strings.TrimSpace(sb.String())
		if s != "" {
			sentences = append(sentences, s)
		}
	}

	// Fallbacks when we couldn't segment well
	if len(sentences) == 0 {
		parts := strings.Split(cleaned, "\n")
		if len(parts) > 1 {
			return parts
		}
		if len(cleaned) > 500 {
			if strings.Contains(cleaned, "  ") {
				return strings.Split(cleaned, "  ")
			}
			return chunkText(cleaned, 100)
		}
		return []string{cleaned}
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

// containsWholeWord checks if text contains a whole word (case insensitive, plural-aware)
func containsWholeWord(text, word string) bool {
	// Match base, base+s, or base+es
	pattern := fmt.Sprintf(`\b(?:%s(?:es|s)?)\b`, regexp.QuoteMeta(word))
	regex := regexp.MustCompile(`(?i)` + pattern)
	return regex.MatchString(text)
}

// HighlightTerms highlights search terms in text with color codes (plural-aware)
func HighlightTerms(text string, searchTerms []string) string {
	const HI = "\033[1;31m" // bold red for stronger, more visible highlighting
	const NC = "\033[0m"

	result := text
	for _, term := range searchTerms {
		// Highlight base, base+s, or base+es as whole words
		pattern := fmt.Sprintf(`\b(?:%s(?:es|s)?)\b`, regexp.QuoteMeta(term))
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
