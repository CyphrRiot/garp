package search

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"find-words/config"
)

// CheckTextContainsAllWords checks if extracted text contains all search words
// in any order, within a distance window (in characters) between the earliest
// and latest matched term positions.
func CheckTextContainsAllWords(text string, words []string, distance int) bool {
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

// CheckTextContainsExcludeWords checks if extracted text contains any exclude words
func CheckTextContainsExcludeWords(text string, excludeWords []string) bool {
	if len(excludeWords) == 0 {
		return false
	}

	contentStr := strings.ToLower(text)

	// Check each exclude word
	for _, word := range excludeWords {
		if containsWholeWord(contentStr, strings.ToLower(word)) {
			return true
		}
	}

	return false
}

// FileInfo represents information about a file
type FileInfo struct {
	Path string
	Size int64
}

// GetDocumentFileCount returns the count of document files that will be searched (pure Go)
func GetDocumentFileCount(fileTypes []string) (int, error) {
	// Parse allowed extensions from patterns like "-g", "*.txt"
	allowed := make(map[string]bool)
	for i := 0; i < len(fileTypes); i++ {
		if fileTypes[i] == "-g" && i+1 < len(fileTypes) {
			i++
			glob := fileTypes[i]
			if strings.HasPrefix(glob, "*.") {
				ext := strings.ToLower(glob[1:]) // ".txt"
				allowed[ext] = true
			}
		}
	}

	count := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Ignore permission errors; keep walking
			return nil
		}
		if d.IsDir() {
			if d.Name() != "." && config.ShouldSkipDirectory(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if len(allowed) > 0 && !allowed[ext] {
			return nil
		}
		count++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// FindFilesWithFirstWord finds all files containing the first search word (pure Go)
func FindFilesWithFirstWord(word string, fileTypes []string) ([]string, error) {
	// Parse allowed extensions from patterns like "-g", "*.txt"
	allowed := make(map[string]bool)
	for i := 0; i < len(fileTypes); i++ {
		if fileTypes[i] == "-g" && i+1 < len(fileTypes) {
			i++
			glob := fileTypes[i]
			if strings.HasPrefix(glob, "*.") {
				ext := strings.ToLower(glob[1:]) // ".txt"
				allowed[ext] = true
			}
		}
	}

	// Precompute lowercased search word for fast ASCII whole-word scan
	wLower := strings.ToLower(word)
	heavy := map[string]bool{
		".pdf":  true,
		".docx": true,
		".odt":  true,
		".msg":  true,
		".eml":  true,
		".mbox": true,
	}
	matches := make([]string, 0, 128)
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Ignore permission errors; keep walking
			return nil
		}
		if d.IsDir() {
			if d.Name() != "." && config.ShouldSkipDirectory(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// Filter by extension if provided
		ext := strings.ToLower(filepath.Ext(path))
		if len(allowed) > 0 && !allowed[ext] {
			return nil
		}

		// Fast first-word check: stream file without extraction
		if heavy[ext] {
			// include heavy binary types as candidates; full check later
			matches = append(matches, path)
			return nil
		}

		// Stream up to maxBytes looking for the first word
		const chunkSize = 64 * 1024
		const maxBytes = 10 * 1024 * 1024
		overlap := 32
		if l := len(wLower) - 1; l > overlap {
			overlap = l
		}
		time.Sleep(2 * time.Millisecond)
		f, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}

		// Early path for small files: read whole file at once, avoid chunk loop
		if st, stErr := f.Stat(); stErr == nil && st.Size() <= chunkSize {
			data, _ := io.ReadAll(f)
			found := asciiIndexWholeWordCI(data, []byte(wLower))
			if found {
				matches = append(matches, path)
			}
			_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
			_ = f.Close()
			return nil
		}

		var total int64
		prev := make([]byte, 0, overlap)
		buf := make([]byte, chunkSize)
		found := false
		for {
			if total >= maxBytes {
				break
			}
			toRead := chunkSize
			if rem := maxBytes - total; rem < int64(toRead) {
				toRead = int(rem)
			}
			n, rErr := f.Read(buf[:toRead])
			if n > 0 {
				combined := append(prev, buf[:n]...)
				if asciiIndexWholeWordCI(combined, []byte(wLower)) {
					found = true
				}
				if n >= overlap {
					prev = append(prev[:0], buf[n-overlap:n]...)
				} else {
					if len(combined) >= overlap {
						prev = append(prev[:0], combined[len(combined)-overlap:]...)
					} else {
						prev = append(prev[:0], combined...)
					}
				}
				total += int64(n)
			}
			if rErr == io.EOF {
				break
			}
			if rErr != nil {
				break
			}
		}

		if found {
			matches = append(matches, path)
		}
		_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
		_ = f.Close()
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return matches, nil
}

// FindFilesWithFirstWordProgress is like FindFilesWithFirstWord but emits per-file discovery progress.
func FindFilesWithFirstWordProgress(word string, fileTypes []string, onProgress func(processed, total int, path string)) ([]string, error) {
	// Parse allowed extensions from patterns like "-g", "*.txt"
	allowed := make(map[string]bool)
	for i := 0; i < len(fileTypes); i++ {
		if fileTypes[i] == "-g" && i+1 < len(fileTypes) {
			i++
			glob := fileTypes[i]
			if strings.HasPrefix(glob, "*.") {
				ext := strings.ToLower(glob[1:]) // ".txt"
				allowed[ext] = true
			}
		}
	}

	// Estimate total and emit initial progress
	total, _ := GetDocumentFileCount(fileTypes)
	if onProgress != nil {
		onProgress(0, total, "")
	}

	wLower := strings.ToLower(word)
	heavy := map[string]bool{
		".pdf":  true,
		".docx": true,
		".odt":  true,
		".msg":  true,
		".eml":  true,
		".mbox": true,
	}

	// Results and synchronization
	matches := make([]string, 0, 128)
	var mu sync.Mutex

	// Bounded worker pool
	workers := 1
	paths := make(chan string, 1024)
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			const chunkSize = 64 * 1024
			const maxBytes = 10 * 1024 * 1024
			overlap := 32
			if l := len(wLower) - 1; l > overlap {
				overlap = l
			}

			for p := range paths {
				time.Sleep(2 * time.Millisecond)
				f, openErr := os.Open(p)
				if openErr != nil {
					continue
				}

				// Early path for small files: read whole file at once, avoid chunk loop
				if st, stErr := f.Stat(); stErr == nil && st.Size() <= chunkSize {
					data, _ := io.ReadAll(f)
					_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
					_ = f.Close()

					found := asciiIndexWholeWordCI(data, []byte(wLower))
					if found {
						mu.Lock()
						matches = append(matches, p)
						mu.Unlock()
					}
					continue
				}

				var readTotal int64
				prev := make([]byte, 0, overlap)
				buf := make([]byte, chunkSize)
				found := false

				for {
					if readTotal >= maxBytes {
						break
					}
					toRead := chunkSize
					if rem := maxBytes - readTotal; rem < int64(toRead) {
						toRead = int(rem)
					}
					n, rErr := f.Read(buf[:toRead])
					if n > 0 {
						combined := append(prev, buf[:toRead]...)
						if asciiIndexWholeWordCI(combined, []byte(wLower)) {
							found = true
						}
						if n >= overlap {
							prev = append(prev[:0], buf[n-overlap:n]...)
						} else {
							if len(combined) >= overlap {
								prev = append(prev[:0], combined[len(combined)-overlap:]...)
							} else {
								prev = append(prev[:0], combined...)
							}
						}
						readTotal += int64(n)
					}
					if rErr == io.EOF {
						break
					}
					if rErr != nil {
						break
					}
				}

				_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
				_ = f.Close()

				if found || (!found && readTotal >= maxBytes) {
					mu.Lock()
					matches = append(matches, p)
					mu.Unlock()
				}
			}
		}()
	}

	processed := 0

	// Walk and stream paths to workers
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() != "." && config.ShouldSkipDirectory(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if len(allowed) > 0 && !allowed[ext] {
			return nil
		}

		processed++
		if onProgress != nil {
			onProgress(processed, total, path)
		}
		// Light throttle to reduce I/O/CPU bursts during discovery
		if processed%250 == 0 {
			time.Sleep(2 * time.Millisecond)
		}

		// Heavy files are added directly; skip worker scan
		if heavy[ext] {
			mu.Lock()
			matches = append(matches, path)
			mu.Unlock()
			return nil
		}

		// Enqueue for worker scanning
		paths <- path
		return nil
	})

	// Close path feed and wait for workers
	close(paths)
	wg.Wait()

	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return matches, nil
}

// StreamContainsAllWords streams a file and returns true if all words are present (unordered, plural-aware, CI).
func StreamContainsAllWordsDecided(filePath string, words []string) (found bool, decided bool) {
	if len(words) == 0 {
		return true, true
	}
	f, err := os.Open(filePath)
	if err != nil {
		return false, true
	}
	defer f.Close()

	// Build plural-aware whole-word regexes (?i)\b(?:word(?:es|s)?)\b
	res := make([]*regexp.Regexp, 0, len(words))
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		pat := fmt.Sprintf(`(?i)\b(?:%s(?:es|s)?)\b`, regexp.QuoteMeta(w))
		res = append(res, regexp.MustCompile(pat))
	}
	if len(res) == 0 {
		return true, true
	}

	const chunkSize = 64 * 1024
	const overlap = 128

	// Align with GetFileContent limits
	stat, statErr := f.Stat()
	var maxBytes int64
	if statErr == nil {
		switch {
		case stat.Size() > 50*1024*1024:
			maxBytes = 10 * 1024 * 1024
		case stat.Size() > 10*1024*1024:
			maxBytes = 5 * 1024 * 1024
		default:
			maxBytes = stat.Size()
		}
	} else {
		maxBytes = 10 * 1024 * 1024
	}

	foundFlags := make([]bool, len(res))
	remaining := len(res)

	var total int64
	prev := make([]byte, 0, overlap)
	buf := make([]byte, chunkSize)
	for {
		if total >= maxBytes {
			// Budget reached; we couldn't decide conclusively
			_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
			return false, false
		}
		toRead := chunkSize
		if rem := maxBytes - total; rem < int64(toRead) {
			toRead = int(rem)
		}
		n, rErr := f.Read(buf[:toRead])
		if n > 0 {
			combined := append(prev, buf[:n]...)
			for i, re := range res {
				if !foundFlags[i] && re.Match(combined) {
					foundFlags[i] = true
					remaining--
					if remaining == 0 {
						_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
						return true, true
					}
				}
			}
			if n >= overlap {
				prev = append(prev[:0], buf[n-overlap:n]...)
			} else {
				if len(combined) >= overlap {
					prev = append(prev[:0], combined[len(combined)-overlap:]...)
				} else {
					prev = append(prev[:0], combined...)
				}
			}
			total += int64(n)
		}
		if rErr == io.EOF {
			// End of file; if not all found, the decision is conclusive
			_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
			return false, true
		}
		if rErr != nil {
			// I/O error; treat as decided false
			_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
			return false, true
		}
	}
}

func StreamContainsAllWords(filePath string, words []string) bool {
	found, _ := StreamContainsAllWordsDecided(filePath, words)
	return found
}

// StreamContainsAllWordsDecidedWithCap streams a file and returns whether all words are present.
// - found = true, decided = true: conclusively found all words
// - found = false, decided = true: conclusively not all words present
// - found = false, decided = false: budget reached; prefilter is undecided (do not skip)
func StreamContainsAllWordsDecidedWithCap(filePath string, words []string, capBytes int64) (bool, bool) {
	if len(words) == 0 {
		return true, true
	}
	f, err := os.Open(filePath)
	if err != nil {
		return false, true
	}
	defer f.Close()

	// Build plural-aware whole-word regexes (?i)\b(?:word(?:es|s)?)\b
	res := make([]*regexp.Regexp, 0, len(words))
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		pat := fmt.Sprintf(`(?i)\b(?:%s(?:es|s)?)\b`, regexp.QuoteMeta(w))
		res = append(res, regexp.MustCompile(pat))
	}
	if len(res) == 0 {
		return true, true
	}

	const chunkSize = 64 * 1024
	const overlap = 128

	// Align with GetFileContent limits, then apply optional capBytes
	stat, statErr := f.Stat()
	var maxBytes int64
	if statErr == nil {
		switch {
		case stat.Size() > 50*1024*1024:
			maxBytes = 10 * 1024 * 1024
		case stat.Size() > 10*1024*1024:
			maxBytes = 5 * 1024 * 1024
		default:
			maxBytes = stat.Size()
		}
	} else {
		maxBytes = 10 * 1024 * 1024
	}
	if capBytes > 0 && capBytes < maxBytes {
		maxBytes = capBytes
	}

	foundFlags := make([]bool, len(res))
	remaining := len(res)

	var total int64
	prev := make([]byte, 0, overlap)
	buf := make([]byte, chunkSize)
	for {
		if total >= maxBytes {
			_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
			return false, false // budget reached; undecided
		}
		toRead := chunkSize
		if rem := maxBytes - total; rem < int64(toRead) {
			toRead = int(rem)
		}
		n, rErr := f.Read(buf[:toRead])
		if n > 0 {
			combined := append(prev, buf[:n]...)
			for i, re := range res {
				if !foundFlags[i] && re.Match(combined) {
					foundFlags[i] = true
					remaining--
					if remaining == 0 {
						_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
						return true, true
					}
				}
			}
			if n >= overlap {
				prev = append(prev[:0], buf[n-overlap:n]...)
			} else {
				if len(combined) >= overlap {
					prev = append(prev[:0], combined[len(combined)-overlap:]...)
				} else {
					prev = append(prev[:0], combined...)
				}
			}
			total += int64(n)
		}
		if rErr == io.EOF {
			_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
			return false, true // EOF: conclusively not all present
		}
		if rErr != nil {
			_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
			return false, true // I/O error: treat as decided false
		}
	}
}

// CheckFileContainsAllWords checks if a file contains all search words
func CheckFileContainsAllWords(filePath string, words []string, distance int, silent bool) (bool, error) {
	// Fast prefilter: require presence of all words before full distance check
	if !StreamContainsAllWords(filePath, words) {
		return false, nil
	}

	content, _, err := GetFileContent(filePath)
	if err != nil {
		return false, err
	}
	return CheckTextContainsAllWords(content, words, distance), nil
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

// StreamContainsWord checks if a file contains a given word using streaming read
func StreamContainsWord(filePath string, word string) bool {
	pattern := fmt.Sprintf(`(?i)\b(?:%s(?:es|s)?)\b`, regexp.QuoteMeta(word))
	re := regexp.MustCompile(pattern)

	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	const chunkSize = 64 * 1024
	const overlap = 128

	// Compute maxBytes consistent with GetFileContent limits
	stat, statErr := f.Stat()
	var maxBytes int64
	if statErr == nil {
		switch {
		case stat.Size() > 50*1024*1024:
			maxBytes = 10 * 1024 * 1024
		case stat.Size() > 10*1024*1024:
			maxBytes = 5 * 1024 * 1024
		default:
			maxBytes = stat.Size()
		}
	} else {
		// Fallback to previous hard cap if stat fails
		maxBytes = 10 * 1024 * 1024
	}
	var total int64
	prev := make([]byte, 0, overlap)
	buf := make([]byte, chunkSize)
	for {
		if total >= maxBytes {
			break
		}
		toRead := chunkSize
		if rem := maxBytes - total; rem < int64(toRead) {
			toRead = int(rem)
		}
		n, rErr := f.Read(buf[:toRead])
		if n > 0 {
			combined := append(prev, buf[:n]...)
			if re.Match(combined) {
				_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
				return true
			}
			if n >= overlap {
				prev = append(prev[:0], buf[n-overlap:n]...)
			} else {
				if len(combined) >= overlap {
					prev = append(prev[:0], combined[len(combined)-overlap:]...)
				} else {
					prev = append(prev[:0], combined...)
				}
			}
			total += int64(n)
		}
		if rErr == io.EOF {
			break
		}
		if rErr != nil {
			break
		}
	}
	_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
	return false
}

// asciiIndexWholeWordCI performs a fast ASCII, case-insensitive, whole-word search
// with basic plural-awareness: matches base, base+s, or base+es.
func asciiIndexWholeWordCI(buf []byte, wordLower []byte) bool {
	if len(wordLower) == 0 || len(buf) < len(wordLower) {
		return false
	}
	isWordChar := func(b byte) bool {
		switch {
		case b >= 'A' && b <= 'Z':
			return true
		case b >= 'a' && b <= 'z':
			return true
		case b >= '0' && b <= '9':
			return true
		default:
			return b == '_'
		}
	}
	toLower := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b | 0x20
		}
		return b
	}

	wl := len(wordLower)
	limit := len(buf) - wl
	for i := 0; i <= limit; i++ {
		// left boundary
		if i > 0 && isWordChar(buf[i-1]) {
			continue
		}

		// try exact base match
		j := 0
		for ; j < wl; j++ {
			if toLower(buf[i+j]) != wordLower[j] {
				break
			}
		}
		if j == wl {
			// check boundary after base
			end := i + wl
			if end >= len(buf) || !isWordChar(buf[end]) {
				return true
			}
			// try 's' plural
			if end < len(buf) && toLower(buf[end]) == 's' {
				endS := end + 1
				if endS >= len(buf) || !isWordChar(buf[endS]) {
					return true
				}
			}
			// try 'es' plural
			if end+1 < len(buf) && toLower(buf[end]) == 'e' && toLower(buf[end+1]) == 's' {
				endES := end + 2
				if endES >= len(buf) || !isWordChar(buf[endES]) {
					return true
				}
			}
		}
	}
	return false
}
