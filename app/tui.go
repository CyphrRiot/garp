package app

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/sys/unix"

	"find-words/config"
	"find-words/search"
)

var startWall time.Time
var progressChan = make(chan progressMsg, 64)
var latestProgress progressMsg
var haveLatestProgress bool
var progressMu sync.Mutex

// progressMsg updates the top progress line while loading.
// Format in View: "⏳ {Stage} [num/total]: filename"
type progressMsg struct {
	Stage string
	Count int
	Total int
	Path  string
}

// Styles (exported styling used by CLI usage/version output too)
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
			Foreground(lipgloss.Color("#565f89"))
)

type model struct {
	// Results and paging
	results       []search.SearchResult
	currentPage   int
	pageSize      int
	totalPages    int
	contentScroll int

	// progress totals
	totalFiles int

	// Session and timing
	searchTime time.Duration
	quitting   bool
	loading    bool

	// Window size
	width  int
	height int

	// Search parameters
	searchWords       []string
	excludeWords      []string
	includeCode       bool
	distance          int
	heavyConcurrency  int
	fileTimeoutBinary int

	// UI state
	confirmSelected string // "yes" or "no"
	memUsageText    string // e.g., " • RAM: XXX MB • CPU: YY%"

	// Background progress (optional)
	progressText string // e.g., "⏳ Processing..."
}

func (m model) Init() tea.Cmd {
	// Start polling progress and kick off the background search immediately.
	return tea.Batch(pollProgress(), m.runSearch(), m.memUsageTick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// While loading, only allow quit
		if m.loading {
			switch msg.String() {
			case "q", "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil
		}

		// Selection navigation for highlighted buttons
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
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
			if m.currentPage < m.totalPages-1 {
				m.currentPage++
			}
			m.contentScroll = 0
			return m, nil
		case "p":
			if m.currentPage > 0 {
				m.currentPage--
			}
			m.contentScroll = 0
			return m, nil

		case "home":
			m.currentPage = 0
			m.contentScroll = 0
			return m, nil
		case "end":
			m.currentPage = m.totalPages - 1
			m.contentScroll = 0
			return m, nil
		case "up", "k":
			m.contentScroll--
			return m, nil
		case "down", "j":
			m.contentScroll++
			return m, nil
		case "pgup":
			m.contentScroll -= 5
			return m, nil
		case "pgdown":
			m.contentScroll += 5
			return m, nil
		}
		return m, nil

	case searchResultMsg:
		// Search completed: store results, compute pages, stop loading
		m.results = msg.results
		m.confirmSelected = "yes"
		m.searchTime = msg.searchTime
		m.totalPages = len(m.results)
		if m.totalPages == 0 {
			m.totalPages = 1
		}
		m.loading = false
		return m, m.memUsageTick()

	case memUsageMsg:
		m.memUsageText = msg.Text
		return m, m.memUsageTick()

	case progressMsg:
		// Update the top progress line (only shown while loading)
		p := msg.Path
		// keep relative path
		m.totalFiles = msg.Total
		m.progressText = fmt.Sprintf("%s [%d/%d]: %s", strings.Title(msg.Stage), msg.Count, msg.Total, p)
		// Keep polling progress while loading
		return m, pollProgress()

	case progressTick:
		// Periodic poll: read the most recent progress snapshot (mutex-protected)
		progressMu.Lock()
		lp := latestProgress
		hv := haveLatestProgress
		progressMu.Unlock()

		if hv {
			p := lp.Path
			// keep relative path
			m.totalFiles = lp.Total
			m.progressText = fmt.Sprintf("%s [%d/%d]: %s", strings.Title(lp.Stage), lp.Count, lp.Total, p)
		}
		return m, pollProgress()
	}
	return m, nil
}

func (m model) View() string {
	width := m.width
	height := m.height
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 30
	}

	if m.quitting {
		return "Goodbye!\n"
	}

	// Build header lines
	var headerLines []string

	// Title
	// ASCII GARP logo with version
	logoTop := " █▀▀ ▄▀█ █▀█ █▀█"
	logoBottom := fmt.Sprintf(" █▄█ █▀█ █▀▄ █▀▀  v%s", version)
	if len(logoTop) < len(logoBottom) {
		logoTop += strings.Repeat(" ", len(logoBottom)-len(logoTop))
	}
	logo := lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Align(lipgloss.Left).Render(logoTop + "\n" + logoBottom)
	headerLines = append(headerLines, "")
	headerLines = append(headerLines, logo)
	headerLines = append(headerLines, "")

	// Search terms (classic)
	{
		var terms []string
		for _, w := range m.searchWords {
			terms = append(terms, fmt.Sprintf("\"%s\"", w))
		}
		headerLines = append(headerLines, subHeaderStyle.Render("🔍 Searching: "+strings.Join(terms, " ")))
	}

	// Total matches at the top
	if !m.loading {
		headerLines = append(headerLines, successStyle.Render(fmt.Sprintf("📋 Matched: %d files", len(m.results))))
	}

	// Target description
	targetDesc := config.GetFileTypeDescription(m.includeCode)
	targetPrefix := "📁 Target: "
	targetStyled := lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	headerLines = append(headerLines, targetStyled.Render(wrapTextWithIndent(targetPrefix, targetDesc, width-4)))

	// Engine line with cores + RAM/CPU live
	engine := fmt.Sprintf("⚙️ Engine: Workers %d%s", m.heavyConcurrency, m.memUsageText)
	engineStyled := lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7"))
	headerLines = append(headerLines, engineStyled.Render(engine))

	// Elapsed search time (always show combined line; freeze after completion)
	var minutes float64
	if m.loading {
		minutes = time.Since(startWall).Minutes()
	} else {
		minutes = m.searchTime.Minutes()
	}
	elapsed := fmt.Sprintf("⏱️ Searched: %.2f minutes • Matched: %d of %d files", minutes, len(m.results), m.totalFiles)
	elapsedStyled := lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68"))
	headerLines = append(headerLines, elapsedStyled.Render(elapsed))

	// moved search terms line above, right after logo

	// Header height (count rendered lines accurately)
	searchInfo := strings.Join(headerLines, "\n")
	headerHeight := strings.Count(searchInfo, "\n") + 1
	// Account explicitly for header, progress, bottom status, and footer heights
	progressHeight := 1 // always reserve progress line space to keep box position stable

	bottomStatusHeight := 1 // reserve a single line for bottom status to reduce blank space

	footerHeight := 1 // footer only

	// Top progress line while loading (above the box)
	var parts []string
	parts = append(parts, searchInfo)
	if m.loading {
		var txt string
		if m.progressText != "" {
			// Expect m.progressText formatted as "[num/total]: filename"
			txt = fmt.Sprintf("⏳ %s", m.progressText)
		} else {
			txt = "⏳ Processing"
		}
		progressStyled := lipgloss.NewStyle().Foreground(lipgloss.Color("#7dcfff"))
		parts = append(parts, progressStyled.Render(txt))
	} else {
		// Reserve the progress row to keep the box fixed when not loading
		parts = append(parts, "")
	}

	// Main content box
	var boxContent string
	if m.loading {
		boxContent = "Searching..."
	} else if len(m.results) == 0 {
		boxContent = "No results found."
	} else {
		// Display current result
		result := m.results[m.currentPage]
		boxContent = fmt.Sprintf("File: %s (%s)\n\n", result.FilePath, formatFileSize(result.FileSize))

		// Add email metadata if available
		if result.EmailSubject != "" {
			boxContent += fmt.Sprintf("Subject: %s\n", result.EmailSubject)
		}
		if result.EmailDate != "" {
			boxContent += fmt.Sprintf("Date: %s\n", result.EmailDate)
		}
		if result.EmailSubject != "" || result.EmailDate != "" {
			boxContent += "\n"
		}

		// Add excerpts (single wrapped line with colored label)
		for i, excerpt := range result.Excerpts {
			label := subHeaderStyle.Render(fmt.Sprintf("Excerpt %d: ", i+1))
			innerWidth := (width - 4) - 6
			if innerWidth < 10 {
				innerWidth = 10
			}
			boxContent += wrapTextWithIndent(label, excerpt, innerWidth) + "\n"
		}

		// Page indicator
		boxContent += fmt.Sprintf("Result %d of %d", m.currentPage+1, len(m.results))
	}

	boxOuterWidth := width - 4
	chromeHeight := 4
	contentHeight := height - headerHeight - progressHeight - bottomStatusHeight - footerHeight - chromeHeight
	if contentHeight < 1 {
		contentHeight = 1
	}

	// Window the box content according to contentScroll to enable vertical scrolling
	lines := strings.Split(boxContent, "\n")
	if m.contentScroll < 0 {
		m.contentScroll = 0
	}
	maxStart := 0
	if len(lines) > contentHeight {
		maxStart = len(lines) - contentHeight
	}
	if m.contentScroll > maxStart {
		m.contentScroll = maxStart
	}
	start := m.contentScroll
	end := start + contentHeight
	if end > len(lines) {
		end = len(lines)
	}
	window := strings.Join(lines[start:end], "\n")
	parts = append(parts, appStyle.Width(boxOuterWidth).Height(contentHeight).Render(window))

	// Non-scrolling bottom status (found count + buttons)
	var bottomStatus string
	if !m.loading && len(m.results) > 0 {
		// Inline highlighted buttons (no border boxes)
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
		bottomStatus = cont
	}

	if bottomStatus != "" {
		parts = append(parts, bottomStatus)
	} else {
		// Reserve the bottom status row to keep the box fixed when no status is shown
		parts = append(parts, "")
	}

	// Footer line
	quitInstruction := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Align(lipgloss.Center).
		Render("🔚 'ENTER' continue • 'q' quit • p: previous • n: next")
	parts = append(parts, quitInstruction)

	return strings.Join(parts, "\n")
}

// Background search command (now exposed on model)
func (m model) runSearch() tea.Cmd {
	// Prepare engine and wire progress callback
	fileTypes := config.BuildRipgrepFileTypes(m.includeCode)
	se := search.NewSearchEngine(
		m.searchWords,
		m.excludeWords,
		fileTypes,
		m.includeCode,
		m.heavyConcurrency,
		m.fileTimeoutBinary,
	)
	se.Silent = true
	// Override default proximity window if provided
	if m.distance > 0 {
		se.Distance = m.distance
	}
	// Stream progress from the engine to the TUI header
	se.OnProgress = func(stage string, processed, total int, path string) {
		progressMu.Lock()
		latestProgress = progressMsg{Stage: stage, Count: processed, Total: total, Path: path}
		haveLatestProgress = true
		progressMu.Unlock()

		// also push to the progress channel; drop oldest if full to keep latest flowing
		msg := progressMsg{Stage: stage, Count: processed, Total: total, Path: path}
		select {
		case progressChan <- msg:
		default:
			select {
			case <-progressChan:
			default:
			}
			select {
			case progressChan <- msg:
			default:
			}
		}
	}

	total, _ := search.GetDocumentFileCount(fileTypes)

	// Emit initial progress and then run the search
	return tea.Batch(
		func() tea.Msg { return progressMsg{Stage: "Discovery", Count: 0, Total: total, Path: ""} },
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

func renderSearchTerms(searchWords, excludeWords []string, width int) string {
	var terms []string
	for _, w := range searchWords {
		terms = append(terms, fmt.Sprintf("\"%s\"", w))
	}
	search := strings.Join(terms, " ")
	if len(excludeWords) > 0 {
		var excludes []string
		for _, w := range excludeWords {
			excludes = append(excludes, fmt.Sprintf("\"%s\"", w))
		}
		search += " (excluding " + strings.Join(excludes, ", ") + ")"
	}
	prefix := "🔍 Searching:"
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68"))
	return styled.Render(wrapTextWithIndent(prefix, search, width))
}

func clipLines(text string, maxLines int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= maxLines {
		return text
	}
	return strings.Join(lines[:maxLines], "\n") + "\n..."
}

func wrapTextWithIndent(prefix, text string, width int) string {
	prefixWidth := lipgloss.Width(prefix)
	indent := strings.Repeat(" ", prefixWidth)
	wrapped := lipgloss.NewStyle().Width(width - prefixWidth).Render(text)
	return prefix + strings.ReplaceAll(wrapped, "\n", "\n"+indent)
}

func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}

func buildDynamicExcerpt(content string, searchTerms []string, maxLen int) string {
	// Simplified excerpt building
	return content[:min(maxLen, len(content))]
}

func highlightTermsANSI(text string, searchTerms []string) string {
	const hi = "\033[1;31m" // bold red
	const nc = "\033[0m"
	result := text
	for _, term := range searchTerms {
		result = strings.ReplaceAll(result, term, hi+term+nc)
	}
	return result
}

func (m model) memUsageTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		// Sample memory and CPU
		mem, cpu := sampleMemoryAndCPU()
		return memUsageMsg{Text: fmt.Sprintf(" • Temporaru %5.1f MB • Total %5.1f MB • CPU %5.1f%%", float64(mem.heap)/(1024*1024), float64(mem.rss)/(1024*1024), cpu)}
	})
}

func pollProgress() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(time.Time) tea.Msg {
		// Always trigger a poll tick; Update will drain and coalesce newest progress message
		return progressTick{}
	})
}

var lastCPUWall time.Time
var lastCPUProc time.Duration
var haveCPUSample bool

func sampleMemoryAndCPU() (mem struct{ heap, rss uint64 }, cpu float64) {
	// Sample memory
	var rusage unix.Rusage
	_ = unix.Getrusage(unix.RUSAGE_SELF, &rusage)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	mem.heap = ms.HeapAlloc
	mem.rss = uint64(rusage.Maxrss * 1024) // KB to bytes

	// Sample CPU (process user+sys time from rusage)
	nowWall := time.Now()
	user := time.Duration(rusage.Utime.Sec)*time.Second + time.Duration(rusage.Utime.Usec)*time.Microsecond
	sys := time.Duration(rusage.Stime.Sec)*time.Second + time.Duration(rusage.Stime.Usec)*time.Microsecond
	nowProc := user + sys
	if haveCPUSample {
		wallDiff := nowWall.Sub(lastCPUWall)
		procDiff := nowProc - lastCPUProc
		if wallDiff > 0 {
			cpu = procDiff.Seconds() / wallDiff.Seconds() * 100
			if cpu < 0 {
				cpu = 0
			}
		}
	}
	lastCPUWall = nowWall
	lastCPUProc = nowProc
	haveCPUSample = true
	return
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func formatFileSize(size int64) string {
	return formatBytes(uint64(size))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Messages for TUI updates
type searchResultMsg struct {
	results    []search.SearchResult
	searchTime time.Duration
}

type memUsageMsg struct {
	Text string
}

type progressTick struct{}
