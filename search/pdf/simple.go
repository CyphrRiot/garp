////go:build pdfcpu
//go:build pdfcpu
// +build pdfcpu

package pdf

import (
	"fmt"
	"os"
	"path/filepath"
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

// ExtractAllTextCapped extracts text from a PDF using pdfcpu and returns a single
// ASCII-normalized string composed of per-page text (joined with newlines).
// - pageCap: maximum number of pages to include (use <=0 for default)
// - perPageCap: maximum bytes of text per page (use <=0 for default)
//
// This function is guarded by the 'pdfcpu' build tag.
func ExtractAllTextCapped(path string, pageCap, perPageCap int) (string, error) {
	// Defaults
	if pageCap <= 0 {
		pageCap = DefaultPageCap
	}
	if perPageCap <= 0 {
		perPageCap = DefaultPerPageCap
	}

	// Panic protection around library call.
	defer func() { _ = recover() }()

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

	// Extract page content into a temporary directory, then parse string literals.
	tmpDir, err := os.MkdirTemp("", "garp_pdfcpu_*")
	if err != nil {
		return "", fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Dump content streams (PDF syntax) for all pages.
	if err := api.ExtractContentFile(path, tmpDir, nil, nil); err != nil {
		return "", fmt.Errorf("pdfcpu ExtractContentFile: %w", err)
	}

	// Read generated files, process in name order, and enforce page cap.
	ents, err := os.ReadDir(tmpDir)
	if err != nil {
		return "", fmt.Errorf("read dir: %w", err)
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })

	var b strings.Builder
	pagesProcessed := 0
	for _, de := range ents {
		if de.IsDir() {
			continue
		}
		if pagesProcessed >= pageCap {
			break
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
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(txt)
		pagesProcessed++
	}

	return b.String(), nil
}
