package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/sys/unix"

	"find-words/config"
	"find-words/search"
)

// Embedded version (overridden via -ldflags "-X main.version=X.Y")
var version = "0.1"
var startWall time.Time

// Global progress streaming
var progressChan = make(chan progressMsg, 256)

// Bubbletea model for garp UI
type model struct {
	// Results and paging
	results     []search.SearchResult
	currentPage int
	pageSize    int
	totalPages  int

	// Session and timing
	searchTime time.Duration
	quitting   bool
	loading    bool

	// Window size
	width  int
	height int

	// Search parameters
	searchWords  []string
	excludeWords []string
	includeCode  bool
	distance     int

	// UI state
	confirmSelected string // "yes" or "no"
	memUsageText    string // " • RAM: XXX MB • CPU: YY%"

	// Background progress (optional)
	progressText string // e.g., "⏳ Processing..."
}

// Styles
var (
	appStyle = lipgloss.NewStyle().
			Padding(1, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7aa2f7"))

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7aa2f7")).
			Align(lipgloss.Center)

	subHeaderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7dcfff")).
			Bold(true)

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a9b1d6"))

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9ece6a")).
			Bold(true)

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#e0af68")).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f7768e")).
			Bold(true)

	separatorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)

// Messages
type searchResultMsg struct {
	results    []search.SearchResult
	searchTime time.Duration
}

type memUsageMsg struct {
	Text string // " • RAM: XXX MB • CPU: YY%"
}

// progressMsg updates the top progress line while loading.
// Format in View: "⏳ {Stage} [num/total]: filename"
// progressMsg updates the top progress line while loading.
// Format in View: "⏳ {Stage} [num/total]: filename"
type progressMsg struct {
	Stage string
	Count int
	Total int
	Path  string
}

// Init: run search in background and start header RAM/CPU ticker
func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.runSearch(),
		m.memUsageTick(),
		pollProgress(),
	)
}

// Background search command
func (m model) runSearch() tea.Cmd {
	// Prepare engine and wire progress callback
	fileTypes := config.BuildRipgrepFileTypes(m.includeCode)
	se := search.NewSearchEngine(
		m.searchWords,
		m.excludeWords,
		fileTypes,
		m.includeCode,
	)
	se.Silent = true
	// Override default proximity window if provided
	if m.distance > 0 {
		se.Distance = m.distance
	}
	// Stream progress from the engine to the TUI header
	se.OnProgress = func(stage string, processed, total int, path string) {
		select {
		case progressChan <- progressMsg{Stage: stage, Count: processed, Total: total, Path: path}:
		default:
		}
	}

	total, _ := search.GetDocumentFileCount(fileTypes)

	// Emit initial progress and then run the search
	return tea.Batch(
		func() tea.Msg { return progressMsg{Count: 0, Total: total, Path: ""} },
		func() tea.Msg {
			start := time.Now()
			results, _ := se.Execute()
			return searchResultMsg{
				results:    results,
				searchTime: time.Since(start),
			}
		},
	)
}

// Update: handle results, keys, window size, and ticks
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case searchResultMsg:
		m.results = msg.results
		m.searchTime = msg.searchTime
		m.loading = false
		if len(m.results) > 0 {
			m.totalPages = (len(m.results) + m.pageSize - 1) / m.pageSize
		}
		return m, nil

	case memUsageMsg:
		m.memUsageText = msg.Text
		return m, m.memUsageTick()

	case progressMsg:
		// Update the top progress line (only shown while loading)
		m.progressText = fmt.Sprintf("%s [%d/%d]: %s", strings.Title(msg.Stage), msg.Count, msg.Total, msg.Path)
		// Keep polling progress while loading
		return m, pollProgress()

	case tea.KeyMsg:
		// While loading, only allow quit
		if m.loading {
			switch msg.String() {
			case "q", "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			default:
				return m, nil
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		// Selection navigation for highlighted buttons
		case "left", "h":
			m.confirmSelected = "yes"
			return m, nil
		case "right", "l":
			m.confirmSelected = "no"
			return m, nil

		case "enter":
			if m.confirmSelected == "no" {
				m.quitting = true
				return m, tea.Quit
			}
			// default/"yes": advance or quit if at end
			if m.currentPage < m.totalPages-1 {
				m.currentPage++
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit

		// Legacy keys
		case "y", "space":
			if m.currentPage < m.totalPages-1 {
				m.currentPage++
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit
		case "n":
			m.quitting = true
			return m, tea.Quit
		case "p":
			if m.currentPage > 0 {
				m.currentPage--
			}
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}

	return m, nil
}

// View: header (logo + info), single bordered box (clipped), bottom status + footer
func (m model) View() string {
	if m.quitting {
		return successStyle.Render("✨ Search session ended")
	}

	// Defaults for size
	width := m.width
	if width <= 0 {
		width = 120
	}
	height := m.height
	if height <= 0 {
		height = 30
	}

	// ASCII Logo with version on second line; pad top line to align
	logoTop := " █▀▀ ▄▀█ █▀█ █▀█"
	logoBottom := fmt.Sprintf(" █▄█ █▀█ █▀▄ █▀▀  v%s", version)
	if len(logoTop) < len(logoBottom) {
		logoTop += strings.Repeat(" ", len(logoBottom)-len(logoTop))
	}
	logo := logoTop + "\n" + logoBottom
	logo = lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Align(lipgloss.Center).Render(logo)

	// Build header info
	var headerLines []string
	headerLines = append(headerLines, "")
	headerLines = append(headerLines, "")
	headerLines = append(headerLines, logo)
	headerLines = append(headerLines, "")

	// Searching for
	headerLines = append(headerLines, subHeaderStyle.Render("🔍 Searching for: "+renderSearchTerms(m.searchWords)))

	// Match count at the top (when available)
	if !m.loading && len(m.results) > 0 {
		headerLines = append(headerLines, successStyle.Render(fmt.Sprintf("📋 Match %d of %d files", m.currentPage+1, len(m.results))))
	}

	// Target line (explicit ext list, PDFs disabled note) — use a different color to stand out
	targetPrefix := "📁 Target: "
	targetDesc := config.GetFileTypeDescription(m.includeCode) + " (PDFs disabled)"
	targetStyled := lipgloss.NewStyle().Foreground(lipgloss.Color("75")) // teal/cyan
	headerLines = append(headerLines, targetStyled.Render(wrapTextWithIndent(targetPrefix, targetDesc, width-4)))

	// Engine line with cores + RAM/CPU live — use a distinct color
	engine := fmt.Sprintf("⚙️ Engine: Workers: 1%s", m.memUsageText)
	engineStyled := lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7")) // Tokyo Night magenta
	headerLines = append(headerLines, engineStyled.Render(engine))
	// Elapsed search time
	var elapsed time.Duration
	var label string
	if m.loading {
		elapsed = time.Since(startWall)
		label = "Searching"
	} else {
		elapsed = m.searchTime
		label = "Search"
	}
	elapsedStyled := lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68")) // Tokyo Night yellow
	headerLines = append(headerLines, elapsedStyled.Render(fmt.Sprintf("⏱️ %s: %.2f minutes", label, elapsed.Minutes())))

	// Default selection to Yes
	if m.confirmSelected == "" {
		m.confirmSelected = "yes"
	}

	searchInfo := lipgloss.JoinVertical(lipgloss.Left, headerLines...)

	// Compute dynamic content height for the box (account for header + bottom + footer) FIRST
	headerHeight := strings.Count(searchInfo, "\n") + 1
	topStatusHeight := 0
	if m.loading {
		topStatusHeight = 2 // progress line + extra headroom to prevent header clipping
	}
	// Reserve 2 lines for non-scrolling bottom (spacer + buttons) if results exist
	footerHintHeight := 0
	if !m.loading && len(m.results) > 0 {
		footerHintHeight = 2
	}
	quitHeight := 3 // blank line + footer + blank line
	boxHeight := height - headerHeight - topStatusHeight - footerHintHeight - quitHeight
	if boxHeight < 8 {
		boxHeight = 8
	}
	// appStyle chrome: vertical (padding 1+1 + border 1+1) = 4; horizontal (padding 2+2 + border 1+1) = 6
	innerHeight := boxHeight - 4
	if innerHeight < 3 {
		innerHeight = 3
	}
	boxOuterWidth := width - 4 // keep a small side margin
	if boxOuterWidth < 20 {
		boxOuterWidth = 20
	}
	innerWidth := boxOuterWidth - 6
	if innerWidth < 10 {
		innerWidth = 10
	}

	// Build box content (loading or results), constrained to contentHeight
	var boxContent string
	if m.loading {
		// Show progress above the box; keep the box empty while loading
		boxContent = ""
	} else {
		// Results view
		var resultsContent strings.Builder

		if len(m.results) == 0 {
			resultsContent.WriteString(warningStyle.Render("🔍 No files found containing all search terms"))
		} else {
			start := m.currentPage * m.pageSize
			end := start + m.pageSize
			if end > len(m.results) {
				end = len(m.results)
			}

			for i := start; i < end; i++ {
				res := m.results[i]

				// Current file indicator inside the box (single concise header)
				fileHdr := fmt.Sprintf("📄 %d/%d", i+1, len(m.results))
				resultsContent.WriteString(successStyle.Render(fileHdr) + "\n")

				// Path and size
				abs := search.GetAbsolutePath(res.FilePath)
				resultsContent.WriteString(infoStyle.Render(wrapTextWithIndent("🔗 ", abs, innerWidth)) + "\n")
				// Email metadata (if available)
				if res.EmailDate != "" || res.EmailSubject != "" {
					var parts []string
					if res.EmailDate != "" {
						parts = append(parts, "Date: "+res.EmailDate)
					}
					if res.EmailSubject != "" {
						parts = append(parts, "Subject: "+res.EmailSubject)
					}
					meta := strings.Join(parts, " • ")
					resultsContent.WriteString(infoStyle.Render(wrapTextWithIndent("🛈 ", meta, innerWidth)) + "\n")
				}
				if res.FileSize > 0 {
					resultsContent.WriteString(infoStyle.Render(fmt.Sprintf("📦 Size: %s", search.FormatFileSize(res.FileSize))) + "\n")
				}

				// Content matches (always show header)
				resultsContent.WriteString(infoStyle.Render("📋 Content matches:") + "\n")

				// Content matches are rendered from precomputed excerpts only to minimize memory usage.
				if len(res.Excerpts) > 0 {
					for _, ex := range res.Excerpts {
						resultsContent.WriteString(wrapTextWithIndent("", ex, innerWidth) + "\n")
					}
				} else {
					resultsContent.WriteString(infoStyle.Render("  (no excerpt provided)") + "\n")
				}
				// Do not add extra blank lines that push content; rely on clipping instead
			}
		}

		// Clip to visible content height
		boxContent = clipLines(resultsContent.String(), innerHeight)
	}

	// Non-scrolling bottom status (found count + buttons)
	var bottomStatus string
	if !m.loading {
		if len(m.results) > 0 {
			// Move found count to header (already shown as Match X of Y), so omit here
			// Add an extra spacer line before the buttons for better visual separation
			space := ""
			// Inline highlighted buttons (no border boxes), similar to Migrate
			yesSel := lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#1a1b26")).
				Background(lipgloss.Color("#9ece6a")).
				Padding(0, 1)
			yesUn := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#9ece6a")).
				Padding(0, 1)
			noSel := lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#c0caf5")).
				Background(lipgloss.Color("#414868")).
				Padding(0, 1)
			noUn := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#565f89")).
				Padding(0, 1)

			var yesBtn, noBtn string
			if m.confirmSelected == "no" {
				yesBtn = yesUn.Render("[ Yes ]")
				noBtn = noSel.Render("[ No ]")
			} else {
				yesBtn = yesSel.Render("[ Yes ]")
				noBtn = noUn.Render("[ No ]")
			}

			cont := infoStyle.Render("Continue? ") + yesBtn + "    " + noBtn
			bottomStatus = lipgloss.JoinVertical(lipgloss.Left, space, cont)
		}
	}

	// Footer line
	quitInstruction := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Align(lipgloss.Center).
		Render("PRESS 🔚 next item • q: to quit • p: previous item")

	// (moved height calculation earlier to size excerpt correctly)

	// Assemble the full view (exactly one bordered box)
	var parts []string
	parts = append(parts, searchInfo)

	// Top progress line while loading (above the box) — use a vivid green to distinguish progress
	if m.loading {
		txt := "⏳ Searching..."
		if m.progressText != "" {
			// Expect m.progressText formatted as "{Stage} [num/total]: filename"
			txt = fmt.Sprintf("⏳ %s", m.progressText)
		}
		progressStyled := lipgloss.NewStyle().Foreground(lipgloss.Color("#7dcfff")) // Tokyo Night cyan
		parts = append(parts, progressStyled.Render(txt))
	}

	parts = append(parts, appStyle.Width(boxOuterWidth).Height(boxHeight).Render(boxContent))
	if bottomStatus != "" {
		parts = append(parts, bottomStatus)
	}
	parts = append(parts, "", quitInstruction, "")

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// Helper: render search terms "quoted"
func renderSearchTerms(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = fmt.Sprintf("\"%s\"", w)
	}
	return strings.Join(quoted, " ")
}

// Helper: clip content to at most max lines
func clipLines(s string, max int) string {
	if max <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	return strings.Join(lines[:max], "\n")
}

// Helper: wrap text so continuation lines are indented to align after a prefix
func wrapTextWithIndent(prefix, text string, totalWidth int) string {
	if totalWidth <= 0 {
		totalWidth = 80
	}
	prefixWidth := runeLen(prefix)
	contentWidth := totalWidth - prefixWidth
	if contentWidth < 20 {
		contentWidth = 20
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return prefix
	}

	var b strings.Builder
	b.WriteString(prefix)
	cur := 0
	for i, w := range words {
		wlen := runeLen(w)
		if cur == 0 {
			b.WriteString(w)
			cur = wlen
		} else {
			if cur+1+wlen <= contentWidth {
				b.WriteString(" ")
				b.WriteString(w)
				cur += 1 + wlen
			} else {
				b.WriteString("\n")
				// One extra space for nicer alignment under prefix
				b.WriteString(strings.Repeat(" ", prefixWidth+1))
				b.WriteString(w)
				cur = wlen
			}
		}
		if i == len(words)-1 {
			break
		}
	}
	return b.String()
}

func runeLen(s string) int { return len([]rune(s)) }

// buildDynamicExcerpt creates a dynamically sized excerpt around the first match of any term,
// trying to expand to sentence boundaries, and then outwards to approximately maxLines * lineWidth characters.
func buildDynamicExcerpt(content string, terms []string, maxLines int, lineWidth int) string {
	if maxLines < 1 || lineWidth < 10 {
		return ""
	}
	targetChars := maxLines * lineWidth
	if targetChars < 120 {
		targetChars = 120
	}

	text := strings.TrimSpace(content)
	if text == "" {
		return ""
	}

	// Find the earliest occurrence of any term (case-insensitive)
	lower := strings.ToLower(text)
	matchStart := -1
	matchEnd := -1
	for _, t := range terms {
		if strings.TrimSpace(t) == "" {
			continue
		}
		tl := strings.ToLower(t)
		pos := strings.Index(lower, tl)
		if pos >= 0 && (matchStart == -1 || pos < matchStart) {
			matchStart = pos
			matchEnd = pos + len(tl)
		}
	}
	// If nothing found, return the head of the document clipped to target
	if matchStart == -1 {
		if len(text) <= targetChars {
			return highlightTermsANSI(text, terms)
		}
		return highlightTermsANSI(text[:targetChars], terms)
	}

	// Expand to sentence boundaries
	left := matchStart
	for left > 0 && text[left] != '.' && text[left] != '!' && text[left] != '?' {
		left--
	}
	if left > 0 {
		left++
		for left < matchStart && (text[left] == ' ' || text[left] == '\n' || text[left] == '\t') {
			left++
		}
	} else {
		left = 0
	}

	right := matchEnd
	for right < len(text) && text[right] != '.' && text[right] != '!' && text[right] != '?' {
		right++
	}
	if right < len(text) {
		right++
	} else {
		right = len(text)
	}

	// Ensure we meet target size by expanding word-wise if needed
	for (right-left) < targetChars && (left > 0 || right < len(text)) {
		expand := (targetChars - (right - left)) / 2
		if expand < 20 {
			expand = 20
		}
		if left > 0 {
			newLeft := left - expand
			if newLeft < 0 {
				newLeft = 0
			}
			// round to word boundary
			for newLeft > 0 && text[newLeft] != ' ' && text[newLeft] != '\n' {
				newLeft--
			}
			left = newLeft
		}
		if right < len(text) {
			newRight := right + expand
			if newRight > len(text) {
				newRight = len(text)
			}
			// round to word boundary
			for newRight < len(text) && newRight > right && text[newRight-1] != ' ' && text[newRight-1] != '\n' {
				newRight++
				if newRight >= len(text) {
					break
				}
			}
			if newRight > len(text) {
				newRight = len(text)
			}
			right = newRight
		}
		if left == 0 && right == len(text) {
			break
		}
	}

	ex := strings.TrimSpace(text[left:right])
	// Normalize whitespace
	ex = strings.ReplaceAll(ex, "\n", " ")
	ex = strings.ReplaceAll(ex, "\t", " ")
	ex = regexp.MustCompile(`\s+`).ReplaceAllString(ex, " ")
	return highlightTermsANSI(ex, terms)
}

// highlightTermsANSI applies a bold red ANSI style to matched whole words (case-insensitive)
func highlightTermsANSI(text string, terms []string) string {
	const hi = "\033[1;31m"
	const nc = "\033[0m"
	out := text
	for _, t := range terms {
		if strings.TrimSpace(t) == "" {
			continue
		}
		pattern := `(?i)\b` + regexp.QuoteMeta(t) + `\b`
		re := regexp.MustCompile(pattern)
		out = re.ReplaceAllStringFunc(out, func(m string) string { return hi + m + nc })
	}
	return out
}

// Mem/CPU ticker
func (m model) memUsageTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		// Go heap
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		heapMB := float64(ms.Alloc) / (1024 * 1024)

		// RSS via /proc/self/statm (Linux)
		rssMB := 0.0
		if data, err := os.ReadFile("/proc/self/statm"); err == nil {
			fields := strings.Fields(string(data))
			if len(fields) >= 2 {
				if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
					rssMB = float64(pages*uint64(os.Getpagesize())) / (1024 * 1024)
				}
			}
		}
		memText := fmt.Sprintf(" • Go Heap: %3.0f MB • Resident: %3.0f MB", heapMB, rssMB)

		// CPU (user+sys vs wall)
		now := time.Now()
		var ru unix.Rusage
		cpuText := ""
		if err := unix.Getrusage(unix.RUSAGE_SELF, &ru); err == nil {
			user := time.Duration(ru.Utime.Sec)*time.Second + time.Duration(ru.Utime.Usec)*time.Microsecond
			sys := time.Duration(ru.Stime.Sec)*time.Second + time.Duration(ru.Stime.Usec)*time.Microsecond
			pct := sampleCPUPercent(now, user+sys, runtime.NumCPU())
			if pct >= 0 {
				cpuText = fmt.Sprintf(" • CPU: %.0f%%", pct)
			}
		}
		return memUsageMsg{Text: memText + cpuText}
	})
}

// Poll next progress message from the global channel
func pollProgress() tea.Cmd {
	return func() tea.Msg {
		if progressChan == nil {
			return progressMsg{Count: 0, Total: 0, Path: ""}
		}
		msg, ok := <-progressChan
		if !ok {
			return progressMsg{Count: 0, Total: 0, Path: ""}
		}
		return msg
	}
}

// CPU sampling state
var (
	lastCPUWall   time.Time
	lastCPUProc   time.Duration
	haveCPUSample bool
)

func sampleCPUPercent(now time.Time, proc time.Duration, cores int) float64 {
	if cores <= 0 {
		cores = 1
	}
	if !haveCPUSample {
		lastCPUWall = now
		lastCPUProc = proc
		haveCPUSample = true
		return -1
	}
	dproc := proc - lastCPUProc
	dwall := now.Sub(lastCPUWall)
	lastCPUProc = proc
	lastCPUWall = now
	if dwall <= 0 {
		return -1
	}
	pct := float64(dproc) / float64(dwall) / float64(cores) * 100.0
	if pct < 0 {
		pct = 0
	}
	return pct
}

// Arguments for CLI flags (used to seed TUI)
type Arguments struct {
	SearchWords  []string
	ExcludeWords []string
	IncludeCode  bool
	Distance     int
}

// parseArguments parses command line args
func parseArguments(args []string) *Arguments {
	res := &Arguments{
		SearchWords:  []string{},
		ExcludeWords: []string{},
		IncludeCode:  false,
		Distance:     0,
	}

	parsingExcludes := false
	expectDistance := false

	for _, a := range args {
		if expectDistance {
			if n, err := strconv.Atoi(a); err == nil && n > 0 {
				res.Distance = n
			}
			expectDistance = false
			continue
		}
		switch a {
		case "--code":
			res.IncludeCode = true
		case "--not":
			parsingExcludes = true
		case "--distance", "-distance":
			expectDistance = true
		case "--help", "-h":
			showUsage()
			os.Exit(0)
		case "--version", "-v":
			showVersion()
			os.Exit(0)
		default:
			if parsingExcludes {
				res.ExcludeWords = append(res.ExcludeWords, a)
			} else {
				res.SearchWords = append(res.SearchWords, a)
			}
		}
	}
	return res
}

// showUsage (basic)
func showUsage() {
	fmt.Println(headerStyle.Render("garp - High-Performance Document Search Tool (Pure Go)"))
	fmt.Println()
	fmt.Printf("%sUSAGE:%s\n", subHeaderStyle.Render("USAGE:"), "")
	fmt.Printf("  garp [--code] [--distance N] word1 word2 [...]\n")
	fmt.Printf("  garp [--code] [--distance N] word1 word2 --not excludeword [...]\n")
	fmt.Println()
}

// showVersion
func showVersion() {
	fmt.Println(headerStyle.Render("garp v" + version))
	fmt.Println("High-Performance Document Search Tool")
	fmt.Println("Pure Go Implementation")
}

// main: seed TUI, run with alt screen
func main() {
	// Parse args
	args := parseArguments(os.Args[1:])
	if len(args.SearchWords) == 0 {
		showUsage()
		os.Exit(1)
	}

	// Seed model
	m := model{
		results:         []search.SearchResult{},
		currentPage:     0,
		pageSize:        1,
		totalPages:      0,
		searchTime:      0,
		quitting:        false,
		loading:         true,
		width:           0,
		height:          0,
		searchWords:     args.SearchWords,
		excludeWords:    args.ExcludeWords,
		includeCode:     args.IncludeCode,
		distance:        args.Distance,
		confirmSelected: "yes",
		memUsageText:    "",
		progressText:    "",
	}

	// Run Bubbletea with alt screen
	startWall = time.Now()
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}
