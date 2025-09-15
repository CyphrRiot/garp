package search

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/emersion/go-mbox"
	"github.com/jhillyerd/enmime"
	"github.com/ledongthuc/pdf"
	"github.com/richardlehane/mscfb"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// Extractor defines the interface for extracting text from binary or encoded document formats
type Extractor interface {
	// ExtractText takes raw file bytes and returns extracted plain text
	ExtractText(data []byte) (string, error)
}

// pdfPagesTruncated counts the number of PDF pages truncated for safety.
var pdfPagesTruncated int64

// PDFPageTextCapBytes is the maximum number of bytes of text we consider per PDF page during scanning.
// This cap is applied defensively to prevent pathological pages from causing excessive memory/time usage.
const PDFPageTextCapBytes = 131072

// PDFPresenceOnlyPathCapped performs a synchronous, bounded presence-only scan on a PDF.
// It returns (found, decided):
//   - found=true, decided=true   => conclusively found all words
//   - found=false, decided=true  => conclusively absent within bounds
//   - found=false, decided=false => cap/time bound reached; do not skip based on this
func PDFPresenceOnlyPathCapped(path string, words []string, maxPages int, maxDur time.Duration) (bool, bool) {
	if len(words) == 0 {
		return true, true
	}
	defer func() { _ = recover() }()

	f, err := os.Open(path)
	if err != nil {
		// Treat file open failure as decided(false) for safety of the scan itself
		return false, true
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return false, true
	}

	reader, err := pdf.NewReader(f, stat.Size())
	if err != nil {
		// Reader construction failed (malformed/corrupt PDF) — undecided to avoid false negatives
		return false, false
	}

	// Safely obtain number of pages
	pages := 0
	func() {
		defer func() { _ = recover() }()
		pages = reader.NumPage()
	}()
	if pages <= 0 {
		return false, false
	}

	// Precompile plural-aware whole-word, case-insensitive regexes
	rs := make([]*regexp.Regexp, len(words))
	found := make([]bool, len(words))
	remaining := len(words)
	for i, w := range words {
		pattern := fmt.Sprintf(`(?i)\b(?:%s(?:es|s)?)\b`, regexp.QuoteMeta(w))
		rs[i] = regexp.MustCompile(pattern)
	}

	// Apply caps
	if maxPages <= 0 || maxPages > pages {
		maxPages = pages
	}
	start := time.Now()
	truncatedEver := false

	for i := 1; i <= maxPages; i++ {
		// Time cap first to avoid deep loops
		if time.Since(start) > maxDur {
			return false, false
		}

		// Extract page text defensively
		var pageText string
		func() {
			defer func() { _ = recover() }()
			page := reader.Page(i)
			if page.V.IsNull() {
				return
			}
			content := page.Content()
			var b strings.Builder
			for _, item := range content.Text {
				b.WriteString(item.S)
				b.WriteByte(' ')
			}
			pageText = b.String()
			if len(pageText) > PDFPageTextCapBytes {
				truncatedEver = true
				atomic.AddInt64(&pdfPagesTruncated, 1)
				pageText = pageText[:PDFPageTextCapBytes]
			}
		}()

		if pageText == "" {
			continue
		}

		for wi, re := range rs {
			if !found[wi] && re.MatchString(pageText) {
				found[wi] = true
				remaining--
				if remaining == 0 {
					return true, true
				}
			}
		}
	}

	// If we completed the bounded page scan within time, it's a decisive negative
	if time.Since(start) <= maxDur && maxPages >= pages {
		if truncatedEver {
			return false, false
		}
		return false, true
	}
	// Otherwise, we hit a bound; treat as undecided
	return false, false
}

// ExtractorRegistry holds extractors for different file types
type ExtractorRegistry struct {
	extractors map[string]Extractor
}

// NewExtractorRegistry creates a new registry with built-in extractors
func NewExtractorRegistry() *ExtractorRegistry {
	reg := &ExtractorRegistry{
		extractors: make(map[string]Extractor),
	}

	// Register built-in extractors
	reg.registerBuiltIns()

	return reg
}

// HTMLExtractor extracts text from .html files
type HTMLExtractor struct{}

// ExtractText implements the Extractor interface for HTML files
func (e *HTMLExtractor) ExtractText(data []byte) (string, error) {
	html := string(data)
	text := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(html, " ")
	// Simple entity decoding
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&apos;", "'")
	return strings.TrimSpace(text), nil
}

// XMLExtractor extracts text from .xml files
type XMLExtractor struct{}

// ExtractText implements the Extractor interface for XML files
func (e *XMLExtractor) ExtractText(data []byte) (string, error) {
	xml := string(data)
	text := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(xml, " ")
	return strings.TrimSpace(text), nil
}

// GetExtractor returns the extractor for a given file extension (without dot)
func (r *ExtractorRegistry) GetExtractor(ext string) (Extractor, bool) {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	extractor, exists := r.extractors[ext]
	return extractor, exists
}

// registerBuiltIns registers the built-in extractors for supported formats
func (r *ExtractorRegistry) registerBuiltIns() {
	// Email formats
	r.extractors["eml"] = &EMLExtractor{}
	r.extractors["mbox"] = &MBOXExtractor{}

	// Binary document formats
	r.extractors["msg"] = &MSGExtractor{}

	// Office document formats
	r.extractors["docx"] = &DOCXExtractor{}
	r.extractors["odt"] = &ODTExtractor{}

	// Web formats
	r.extractors["html"] = &HTMLExtractor{}
	r.extractors["xml"] = &XMLExtractor{}

	// Other
	r.extractors["rtf"] = &RTFExtractor{}

	// PDFs (on-demand extraction; bulk path remains non-extractive)
	r.extractors["pdf"] = &PDFExtractor{}
}

// IsBinaryFormat checks if a file extension requires text extraction
func IsBinaryFormat(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".msg", ".docx", ".odt", ".rtf", ".pdf":
		return true
	case ".eml", ".mbox":
		// EML/MBOX can be text but often encoded
		return true
	default:
		return false
	}
}

// EMLExtractor extracts text from .eml files (MIME messages)
type EMLExtractor struct{}

// ExtractText implements the Extractor interface for EML files
func (e *EMLExtractor) ExtractText(data []byte) (string, error) {
	// Parse the MIME message
	env, err := enmime.ReadEnvelope(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("failed to parse EML: %w", err)
	}

	// Prefer plain text, fallback to HTML if plain text is empty
	text := env.Text
	if text == "" && env.HTML != "" {
		// Strip HTML tags for plain text
		text = stripHTMLTags(env.HTML)
	}

	// Clean up excessive whitespace
	text = strings.TrimSpace(text)
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")

	return text, nil
}

// stripHTMLTags removes HTML tags from text (simple implementation)
func stripHTMLTags(html string) string {
	// Remove HTML tags
	tagRegex := regexp.MustCompile(`<[^>]*>`)
	text := tagRegex.ReplaceAllString(html, " ")

	// Decode HTML entities
	entityRegex := regexp.MustCompile(`&[a-zA-Z0-9#]*;`)
	text = entityRegex.ReplaceAllStringFunc(text, func(entity string) string {
		switch entity {
		case "&amp;":
			return "&"
		case "&lt;":
			return "<"
		case "&gt;":
			return ">"
		case "&quot;":
			return "\""
		case "&apos;":
			return "'"
		default:
			return " "
		}
	})

	return text
}

// MBOXExtractor extracts text from .mbox files (collections of MIME messages)
type MBOXExtractor struct{}

// ExtractText implements the Extractor interface for MBOX files
func (e *MBOXExtractor) ExtractText(data []byte) (string, error) {
	reader := mbox.NewReader(bytes.NewReader(data))
	var text strings.Builder

	emlExtractor := &EMLExtractor{}

	for {
		msg, err := reader.NextMessage()
		if err != nil {
			break
		}
		content, err := io.ReadAll(msg)
		if err != nil {
			continue
		}
		emlData := content
		extracted, err := emlExtractor.ExtractText(emlData)
		if err != nil {
			continue
		}
		text.WriteString(extracted)
		text.WriteString("\n---\n")
	}

	if text.Len() == 0 {
		return string(data), nil
	}
	return text.String(), nil
}

// PDFExtractor extracts text from .pdf files
type PDFExtractor struct{}

// ExtractText implements the Extractor interface for PDF files
func (e *PDFExtractor) ExtractText(data []byte) (out string, err error) {
	// Default to raw content so we always return something readable on failure.
	out = string(data)

	// Guard against any panics from the PDF library.
	defer func() {
		if r := recover(); r != nil {
			// Keep the default 'out' (raw content) and no error.
			err = nil
		}
	}()

	reader, rerr := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if rerr != nil {
		// Fall back to raw content on reader construction error.
		return out, nil
	}

	var b strings.Builder

	// Safely obtain number of pages (library may panic on malformed PDFs).
	pages := 0
	func() {
		defer func() { _ = recover() }()
		pages = reader.NumPage()
	}()

	if pages <= 0 {
		return out, nil
	}

	// Try plain text extraction first; fallback to per-page scan
	if plain, gerr := reader.GetPlainText(); gerr == nil {
		pb, _ := io.ReadAll(plain)
		s := strings.TrimSpace(string(pb))
		if s != "" {
			return s, nil
		}
	}

	// Extract text page-by-page with panic protection for each page.
	for i := 1; i <= pages; i++ {
		func() {
			defer func() { _ = recover() }()
			page := reader.Page(i)
			if page.V.IsNull() {
				return
			}
			content := page.Content()
			for _, item := range content.Text {
				b.WriteString(item.S)
				b.WriteString(" ")
			}
			b.WriteString("\n")
		}()
	}

	extracted := strings.TrimSpace(b.String())
	if extracted == "" {
		return out, nil
	}
	return extracted, nil
}

// PDFContainsAllWordsNoDistance quickly checks if ALL words appear anywhere in the PDF
// (unordered, whole-word, case-insensitive) without performing a full text extraction.
// It returns true as soon as all words are detected, otherwise false.
func PDFContainsAllWordsNoDistance(data []byte, words []string) bool {
	if len(words) == 0 {
		return true
	}

	// Catch any panics from the PDF library and treat as non-match.
	defer func() {
		_ = recover()
	}()

	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}

	// Safely obtain number of pages
	pages := 0
	func() {
		defer func() { _ = recover() }()
		pages = reader.NumPage()
	}()
	if pages <= 0 {
		return false
	}

	// Precompile whole-word, case-insensitive regexes for each word
	rs := make([]*regexp.Regexp, len(words))
	found := make([]bool, len(words))
	remaining := len(words)
	for i, w := range words {
		pattern := fmt.Sprintf(`(?i)\b(?:%s(?:es|s)?)\b`, regexp.QuoteMeta(w))
		rs[i] = regexp.MustCompile(pattern)
	}

	// Scan page by page; mark words as we find them; early exit once all are found
	for i := 1; i <= pages; i++ {
		pageAllFound := false

		func() {
			defer func() { _ = recover() }()
			page := reader.Page(i)
			if page.V.IsNull() {
				return
			}
			content := page.Content()

			// Build a lightweight page text for matching
			var b strings.Builder
			for _, item := range content.Text {
				b.WriteString(item.S)
				b.WriteByte(' ')
			}
			pageText := b.String()

			for wi, re := range rs {
				if !found[wi] && re.MatchString(pageText) {
					found[wi] = true
					remaining--
					if remaining == 0 {
						pageAllFound = true
						break
					}
				}
			}
		}()

		if pageAllFound {
			return true
		}
	}

	return false
}

// PDFHasAllWordsWithinDistanceNoExtract checks if ALL words appear within a distance window
// (unordered, whole-word, case-insensitive) by scanning pages without doing a full extraction.
// Returns true as soon as a qualifying window is found, otherwise false.
func PDFHasAllWordsWithinDistanceNoExtract(data []byte, words []string, distance int) bool {
	if len(words) == 0 {
		return true
	}
	if len(words) == 1 {
		// Reuse presence-only check for the single-word case
		return PDFContainsAllWordsNoDistance(data, words)
	}

	defer func() { _ = recover() }()

	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}

	// Safely obtain number of pages (guarding against panics)
	pages := 0
	func() {
		defer func() { _ = recover() }()
		pages = reader.NumPage()
	}()
	if pages <= 0 {
		return false
	}
	start := time.Now()
	maxDur := 1500 * time.Millisecond

	// Precompile regexes for each search term: whole-word, case-insensitive
	type match struct {
		pos       int
		wordIndex int
	}
	regexes := make([]*regexp.Regexp, len(words))
	for i, w := range words {
		pattern := fmt.Sprintf(`(?i)\b(?:%s(?:es|s)?)\b`, regexp.QuoteMeta(w))
		regexes[i] = regexp.MustCompile(pattern)
	}

	// Streaming sliding window across pages (bounded memory)
	offset := 0
	required := len(words)
	counts := make(map[int]int, required)
	covered := 0
	window := make([]match, 0, 128)

	for i := 1; i <= pages; i++ {
		// Page-safe scope with panic recovery
		var pageText string
		func() {
			defer func() { _ = recover() }()
			page := reader.Page(i)
			if page.V.IsNull() {
				return
			}
			content := page.Content()

			var b strings.Builder
			for _, item := range content.Text {
				b.WriteString(item.S)
				b.WriteByte(' ')
			}
			pageText = b.String()
		}()

		if pageText == "" {
			// Keep order even if page text couldn't be read
			offset += 1
			continue
		}

		// Gather matches for this page only, with global offset
		pageMatches := make([]match, 0, 32)
		for wi, re := range regexes {
			idxs := re.FindAllStringIndex(pageText, -1)
			for _, idx := range idxs {
				pageMatches = append(pageMatches, match{pos: offset + idx[0], wordIndex: wi})
			}
		}
		// Sort this page's matches by position
		if len(pageMatches) > 1 {
			sort.Slice(pageMatches, func(i, j int) bool { return pageMatches[i].pos < pageMatches[j].pos })
		}

		// Extend the sliding window with current page matches, shrinking as needed
		for _, m := range pageMatches {
			// mid-loop time guard
			if time.Since(start) > maxDur {
				return false
			}
			// push right
			window = append(window, m)
			if counts[m.wordIndex] == 0 {
				covered++
			}
			counts[m.wordIndex]++

			// shrink left while window width exceeds distance
			for len(window) > 0 && (window[len(window)-1].pos-window[0].pos) > distance {
				left := window[0]
				counts[left.wordIndex]--
				if counts[left.wordIndex] == 0 {
					covered--
				}
				window = window[1:]
			}

			// all words covered within distance
			if covered == required {
				return true
			}
		}

		// Advance global offset by page length + one space and help GC
		offset += len(pageText) + 1
		pageText = ""
	}

	return false
}

// PDFContainsAllWordsNoDistancePath streams a PDF from disk to check if ALL words
// appear anywhere (unordered, whole-word, case-insensitive) without full extraction.
func PDFContainsAllWordsNoDistancePath(path string, words []string) bool {
	if len(words) == 0 {
		return true
	}
	defer func() { _ = recover() }()

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return false
	}

	reader, err := pdf.NewReader(f, stat.Size())
	if err != nil {
		return false
	}

	pages := 0
	func() {
		defer func() { _ = recover() }()
		pages = reader.NumPage()
	}()
	if pages <= 0 {
		return false
	}

	rs := make([]*regexp.Regexp, len(words))
	found := make([]bool, len(words))
	remaining := len(words)

	for i, w := range words {
		pattern := fmt.Sprintf(`(?i)\b(?:%s(?:es|s)?)\b`, regexp.QuoteMeta(w))
		rs[i] = regexp.MustCompile(pattern)
	}

	for i := 1; i <= pages; i++ {
		pageAllFound := false

		func() {
			defer func() { _ = recover() }()
			page := reader.Page(i)
			if page.V.IsNull() {
				return
			}
			content := page.Content()

			var b strings.Builder
			for _, item := range content.Text {
				b.WriteString(item.S)
				b.WriteByte(' ')
			}
			pageText := b.String()
			if len(pageText) > PDFPageTextCapBytes {
				atomic.AddInt64(&pdfPagesTruncated, 1)
				pageText = pageText[:PDFPageTextCapBytes]
			}

			for wi, re := range rs {
				if !found[wi] && re.MatchString(pageText) {
					found[wi] = true
					remaining--
					if remaining == 0 {
						pageAllFound = true
						break
					}
				}
			}
		}()

		if pageAllFound {
			return true
		}
	}

	return false
}

// PDFHasAllWordsWithinDistanceNoExtractPath streams a PDF from disk to check if ALL
// words appear within the specified distance window (unordered, whole-word, CI)
// without full extraction. Bounded memory via per-page sliding window.
func PDFHasAllWordsWithinDistanceNoExtractPath(path string, words []string, distance int) bool {
	if len(words) == 0 {
		return true
	}
	if len(words) == 1 {
		return PDFContainsAllWordsNoDistancePath(path, words)
	}

	defer func() { _ = recover() }()

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return false
	}

	reader, err := pdf.NewReader(f, stat.Size())
	if err != nil {
		return false
	}

	pages := 0
	func() {
		defer func() { _ = recover() }()
		pages = reader.NumPage()
	}()
	if pages <= 0 {
		return false
	}
	start := time.Now()
	maxDur := 1500 * time.Millisecond
	maxPages := 200

	type match struct {
		pos       int
		wordIndex int
	}

	// Precompile per-word regex
	regexes := make([]*regexp.Regexp, len(words))
	for i, w := range words {
		pattern := fmt.Sprintf(`(?i)\b(?:%s(?:es|s)?)\b`, regexp.QuoteMeta(w))
		regexes[i] = regexp.MustCompile(pattern)
	}

	// Streaming sliding window across pages (bounded memory)
	offset := 0
	required := len(words)
	counts := make(map[int]int, required)
	covered := 0
	window := make([]match, 0, 128)

	for i := 1; i <= pages; i++ {
		if i > maxPages || time.Since(start) > maxDur {
			return false
		}
		var pageText string
		func() {
			defer func() { _ = recover() }()
			page := reader.Page(i)
			if page.V.IsNull() {
				return
			}
			content := page.Content()

			var b strings.Builder
			for _, item := range content.Text {
				b.WriteString(item.S)
				b.WriteByte(' ')
			}
			pageText = b.String()
			if len(pageText) > PDFPageTextCapBytes {
				atomic.AddInt64(&pdfPagesTruncated, 1)
				pageText = pageText[:PDFPageTextCapBytes]
			}
		}()

		if pageText == "" {
			offset += 1
			continue
		}

		// Per-page matches with global offset
		pageMatches := make([]match, 0, 32)
		for wi, re := range regexes {
			idxs := re.FindAllStringIndex(pageText, -1)
			for _, idx := range idxs {
				pageMatches = append(pageMatches, match{pos: offset + idx[0], wordIndex: wi})
			}
		}
		if len(pageMatches) > 1 {
			sort.Slice(pageMatches, func(i, j int) bool { return pageMatches[i].pos < pageMatches[j].pos })
		}

		// Extend sliding window with current page matches
		for _, m := range pageMatches {
			window = append(window, m)
			if counts[m.wordIndex] == 0 {
				covered++
			}
			counts[m.wordIndex]++

			// shrink left while window width exceeds distance
			for len(window) > 0 && (window[len(window)-1].pos-window[0].pos) > distance {
				left := window[0]
				counts[left.wordIndex]--
				if counts[left.wordIndex] == 0 {
					covered--
				}
				window = window[1:]
			}

			if covered == required {
				return true
			}
		}

		// Advance offset; help GC
		offset += len(pageText) + 1
		pageText = ""
	}

	return false
}

// MSGExtractor extracts text from .msg files (Outlook messages)
type MSGExtractor struct{}

// ExtractText implements the Extractor interface for MSG files
func (e *MSGExtractor) ExtractText(data []byte) (string, error) {
	// Attempt to parse the OLE compound file and extract Unicode Subject/Body first.
	if cf, err := mscfb.New(bytes.NewReader(data)); err == nil {
		streams := make(map[string][]byte)
		for ent, err2 := cf.Next(); err2 == nil; ent, err2 = cf.Next() {
			name := ent.Name
			// Read the entire stream content
			b, _ := io.ReadAll(ent)
			if len(b) > 0 {
				streams[name] = b
			}
		}

		// Helper: prefer Unicode (001F), then ANSI (001E), then binary 0102 (for HTML)
		findStream := func(keys ...string) ([]byte, bool) {
			for _, k := range keys {
				if v, ok := streams[k]; ok && len(v) > 0 {
					return v, true
				}
			}
			return nil, false
		}
		// Helper: decode text from MSG stream, UTF-16 aware with ASCII fallback
		decodeMSGText := func(b []byte) string {
			if s, ok := tryDecodeUTF16BestEffort(b); ok {
				return strings.TrimSpace(s)
			}
			s := regexp.MustCompile(`\s+`).ReplaceAllString(string(b), " ")
			return strings.TrimSpace(s)
		}

		var subject, body string

		// PR_SUBJECT: 0037 (Unicode 001F; ANSI 001E)
		if b, ok := findStream("__substg1.0_0037001F", "__substg1.0_0037001E"); ok {
			subject = decodeMSGText(b)
		}
		// PR_BODY: 1000 (Unicode 001F; ANSI 001E)
		if b, ok := findStream("__substg1.0_1000001F", "__substg1.0_1000001E"); ok {
			body = decodeMSGText(b)
		}
		// PR_HTML: 1013 (Unicode 001F; ANSI 001E; sometimes 0102 binary); use as fallback for body
		if body == "" {
			if b, ok := findStream("__substg1.0_1013001F", "__substg1.0_1013001E", "__substg1.0_10130102"); ok {
				html := decodeMSGText(b)
				if html == "" {
					html = string(b)
				}
				body = strings.TrimSpace(stripHTMLTags(html))
			}
		}

		if subject != "" || body != "" {
			out := strings.TrimSpace(strings.TrimSpace(subject) + "\n\n" + strings.TrimSpace(body))
			out = regexp.MustCompile(`\s+`).ReplaceAllString(out, " ")
			return out, nil
		}
	}

	// Fallback: best-effort UTF-16, then ASCII salvage (spaces for non-printables)
	if s, ok := tryDecodeUTF16BestEffort(data); ok {
		return strings.TrimSpace(s), nil
	}
	buf := make([]rune, 0, len(data))
	for _, b := range data {
		if b == 0x09 || b == 0x0a || b == 0x0d || (b >= 0x20 && b <= 0x7e) {
			buf = append(buf, rune(b))
		} else {
			buf = append(buf, ' ')
		}
	}
	out := regexp.MustCompile(`\s+`).ReplaceAllString(string(buf), " ")
	return strings.TrimSpace(out), nil
}

// tryDecodeUTF16BestEffort attempts BOM-aware UTF-16 decoding, then heuristic LE/BE.
func tryDecodeUTF16BestEffort(b []byte) (string, bool) {
	// Prefer BOM-aware decode (handles LE/BE automatically if BOM present)
	{
		r := transform.NewReader(bytes.NewReader(b), unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder())
		s, err := io.ReadAll(r)
		if err == nil {
			str := strings.TrimSpace(string(s))
			if str != "" {
				return str, true
			}
		}
	}

	// Heuristic: many nulls on odd bytes => likely UTF-16LE without BOM
	if isLikelyUTF16LE(b) {
		r := transform.NewReader(bytes.NewReader(b), unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder())
		s, err := io.ReadAll(r)
		if err == nil {
			str := strings.TrimSpace(string(s))
			if str != "" {
				return str, true
			}
		}
	}

	// Heuristic: many nulls on even bytes => likely UTF-16BE without BOM
	if isLikelyUTF16BE(b) {
		r := transform.NewReader(bytes.NewReader(b), unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder())
		s, err := io.ReadAll(r)
		if err == nil {
			str := strings.TrimSpace(string(s))
			if str != "" {
				return str, true
			}
		}
	}

	return "", false
}

func isLikelyUTF16LE(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	zeros := 0
	slots := 0
	for i := 1; i < len(b); i += 2 {
		slots++
		if b[i] == 0x00 {
			zeros++
		}
	}
	return slots > 0 && float64(zeros) >= 0.30*float64(slots)
}

func isLikelyUTF16BE(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	zeros := 0
	slots := 0
	for i := 0; i < len(b); i += 2 {
		slots++
		if b[i] == 0x00 {
			zeros++
		}
	}
	return slots > 0 && float64(zeros) >= 0.30*float64(slots)
}

// DOCXExtractor extracts text from .docx files (Office Open XML)
type DOCXExtractor struct{}

// ExtractText implements the Extractor interface for DOCX files
func (e *DOCXExtractor) ExtractText(data []byte) (string, error) {
	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return string(data), nil
	}

	for _, file := range zipReader.File {
		if file.Name == "word/document.xml" {
			rc, err := file.Open()
			if err != nil {
				continue
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				continue
			}
			text := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(string(content), " ")
			return strings.TrimSpace(text), nil
		}
	}

	return string(data), nil
}

// ODTExtractor extracts text from .odt files (OpenDocument Text)
type ODTExtractor struct{}

// ExtractText implements the Extractor interface for ODT files
func (e *ODTExtractor) ExtractText(data []byte) (string, error) {
	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return string(data), nil
	}

	for _, file := range zipReader.File {
		if file.Name == "content.xml" {
			rc, err := file.Open()
			if err != nil {
				continue
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				continue
			}
			text := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(string(content), " ")
			return strings.TrimSpace(text), nil
		}
	}

	return string(data), nil
}

// RTFExtractor extracts text from .rtf files (Rich Text Format)
type RTFExtractor struct{}

// ExtractText implements the Extractor interface for RTF files
func (e *RTFExtractor) ExtractText(data []byte) (string, error) {
	text := string(data)

	// Remove RTF control words (simple regex approach)
	rtfControlRegex := regexp.MustCompile(`\\[a-z]+\d*`)
	text = rtfControlRegex.ReplaceAllString(text, "")

	// Remove braces
	text = strings.ReplaceAll(text, "{", "")
	text = strings.ReplaceAll(text, "}", "")

	// Clean up excessive whitespace
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")

	return strings.TrimSpace(text), nil
}
