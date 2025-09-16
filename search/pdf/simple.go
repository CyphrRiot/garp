////go:build pdfcpu
//go:build pdfcpu
// +build pdfcpu

package pdf

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// Default caps for PDF text extraction.
const (
	DefaultPageCap    = 200        // maximum number of pages to process
	DefaultPerPageCap = 128 * 1024 // 128 KiB per-page text cap
)

// checkTextContainsAllWords checks if all words appear within the distance window in the text.
func checkTextContainsAllWords(text string, words []string, distance int) bool {
	if len(words) == 0 {
		return true
	}

	contentStr := strings.ToLower(text)

	// Single-term case: just check presence quickly
	if len(words) == 1 {
		pattern := fmt.Sprintf(`\b(?:%s(?:es|s)?)\b`, regexp.QuoteMeta(strings.ToLower(words[0])))
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
		pattern := fmt.Sprintf(`\b(?:%s(?:es|s)?)\b`, regexp.QuoteMeta(strings.ToLower(word)))
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

// asciiNormalize collapses all non-printable or non-ASCII runes to space and
// then normalizes whitespace to single spaces.
func asciiNormalize(s string) string {
	ascii := strings.Map(func(r rune) rune {
		if r > 127 || !unicode.IsPrint(r) {
			return ' '
		}
		return r
	}, s)
	// Collapse whitespace
	return strings.Join(strings.Fields(ascii), " ")
}

// ExtractAllTextCapped extracts text from a PDF using pdfcpu with incremental batches and short-circuiting.
// Returns the extracted text, a boolean indicating if all words are within the distance window, and any error.
// - pageCap: maximum number of pages to include (use <=0 for default)
// - perPageCap: maximum bytes of text per page (use <=0 for default)
// - words: search words
// - window: distance window
//
// This function is guarded by the 'pdfcpu' build tag.
func ExtractAllTextCapped(path string, pageCap, perPageCap int, words []string, window int) (string, bool, error) {
	// Defaults
	if pageCap <= 0 {
		pageCap = DefaultPageCap
	}
	if perPageCap <= 0 {
		perPageCap = DefaultPerPageCap
	}

	// Panic protection around library call.
	defer func() { _ = recover() }()

	// Get total page count
	pageCount, err := api.PageCountFile(path)
	if err != nil {
		return "", false, fmt.Errorf("page count: %w", err)
	}

	// Simple string literal parser for PDF content streams.
	// Collects text within balanced parentheses, honoring backslash escapes, and caps output size.
	parsePDFStringLiterals := func(s string, maxOut int) string {
		var out strings.Builder
		depth := 0
		escape := false
		in := false
		for i := 0; i < len(s); i++ {
			c := s[i]
			if !in {
				if c == '(' {
					in = true
					depth = 1
					continue
				}
				continue
			}
			if escape {
				out.WriteByte(c)
				escape = false
				if out.Len() >= maxOut {
					return out.String()
				}
				continue
			}
			switch c {
			case '\\':
				escape = true
			case '(':
				depth++
				out.WriteByte(c)
			case ')':
				depth--
				if depth == 0 {
					in = false
					out.WriteByte(' ')
				} else {
					out.WriteByte(c)
				}
			default:
				out.WriteByte(c)
			}
			if out.Len() >= maxOut {
				return out.String()
			}
		}
		return out.String()
	}

	batchSize := 32
	var aggregated strings.Builder

	for start := 1; start <= pageCount && start <= pageCap; start += batchSize {
		end := start + batchSize - 1
		if end > pageCount {
			end = pageCount
		}
		if end > pageCap {
			end = pageCap
		}

		pages := []string{fmt.Sprintf("%d-%d", start, end)}

		// Extract page content into a temporary directory for this batch.
		tmpDir, err := os.MkdirTemp("", "garp_pdfcpu_*")
		if err != nil {
			return "", false, fmt.Errorf("temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)

		// Dump content streams (PDF syntax) for the page range.
		if err := api.ExtractContentFile(path, tmpDir, pages, nil); err != nil {
			return "", false, fmt.Errorf("pdfcpu ExtractContentFile: %w", err)
		}

		// Read generated files, process in name order.
		ents, err := os.ReadDir(tmpDir)
		if err != nil {
			return "", false, fmt.Errorf("read dir: %w", err)
		}
		sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })

		for _, de := range ents {
			if de.IsDir() {
				continue
			}
			fp := filepath.Join(tmpDir, de.Name())
			data, _ := os.ReadFile(fp)
			if len(data) == 0 {
				continue
			}

			// Parse simple string literals, normalize to ASCII, and cap per-page output.
			raw := parsePDFStringLiterals(string(data), perPageCap)
			txt := asciiNormalize(raw)
			if len(txt) > perPageCap {
				txt = txt[:perPageCap]
			}
			if txt == "" {
				continue
			}
			if aggregated.Len() > 0 {
				aggregated.WriteByte('\n')
			}
			aggregated.WriteString(txt)
		}

		// Check if the aggregated text so far contains all words within the distance window.
		currentText := aggregated.String()
		if checkTextContainsAllWords(currentText, words, window) {
			return currentText, true, nil
		}
	}

	return aggregated.String(), false, nil
}
