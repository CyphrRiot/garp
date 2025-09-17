package app

// TODO: Add support for --only <type> (e.g., --only pdf)
// - CLI parsing: parse and store an "onlyType" (single extension) mutually exclusive with --code and extension excludes.
// - Discovery: restrict file type globs to only that extension when set.
// - UI: reflect the constraint in the "Target" header line to stay truthful.

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"find-words/search"
)

var version = "0.2"

// Arguments for CLI flags (used to seed TUI)
type Arguments struct {
	SearchWords       []string
	ExcludeWords      []string
	IncludeCode       bool
	Distance          int
	HeavyConcurrency  int
	FilterWorkers     int
	FileTimeoutBinary int
}

// parseArguments parses command line args
func parseArguments(args []string) *Arguments {
	result := &Arguments{
		SearchWords:       []string{},
		ExcludeWords:      []string{},
		IncludeCode:       false,
		Distance:          0,
		HeavyConcurrency:  2,
		FilterWorkers:     2,
		FileTimeoutBinary: 1000,
	}

	parsingExcludes := false
	expectDistance := false
	expectHeavy := false
	expectTimeout := false
	expectWorkers := false
	heavyProvided := false

	for _, a := range args {
		if expectDistance {
			if n, err := strconv.Atoi(a); err == nil && n > 0 {
				result.Distance = n
			}
			expectDistance = false
			continue
		}
		if expectHeavy {
			if n, err := strconv.Atoi(a); err == nil && n > 0 {
				result.HeavyConcurrency = n
				heavyProvided = true
			}
			expectHeavy = false
			continue
		}
		if expectTimeout {
			if n, err := strconv.Atoi(a); err == nil && n > 0 {
				result.FileTimeoutBinary = n
			}
			expectTimeout = false
			continue
		}
		if expectWorkers {
			if n, err := strconv.Atoi(a); err == nil && n > 0 {
				result.FilterWorkers = n
			}
			expectWorkers = false
			continue
		}
		switch a {
		case "--code":
			result.IncludeCode = true
		case "--not":
			parsingExcludes = true
		case "--distance", "-distance":
			expectDistance = true
		case "--heavy-concurrency":
			expectHeavy = true
		case "--file-timeout-binary":
			expectTimeout = true
		case "--workers", "-workers":
			expectWorkers = true
		case "--help", "-h":
			showUsage()
			os.Exit(0)
		case "--version", "-v":
			showVersion()
			os.Exit(0)
		default:
			if parsingExcludes {
				result.ExcludeWords = append(result.ExcludeWords, a)
			} else {
				result.SearchWords = append(result.SearchWords, a)
			}
		}
	}

	// Auto-derive HeavyConcurrency when not explicitly provided: base on workers and available RAM
	if !heavyProvided {
		// default derived heavy = max(1, workers/2)
		workers := result.FilterWorkers
		derived := workers / 2
		if derived < 1 {
			derived = 1
		}
		// Derive an upper bound from system RAM (~ one heavy slot per 4 GiB), clamp to [1,8]
		memAvailKB := int64(0)
		if b, err := os.ReadFile("/proc/meminfo"); err == nil {
			lines := strings.Split(string(b), "\n")
			// Prefer MemAvailable; fallback to MemFree if unavailable
			for _, ln := range lines {
				if strings.HasPrefix(ln, "MemAvailable:") {
					fields := strings.Fields(ln)
					if len(fields) >= 2 {
						if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
							memAvailKB = kb
						}
					}
					break
				}
			}
			if memAvailKB == 0 {
				for _, ln := range lines {
					if strings.HasPrefix(ln, "MemFree:") {
						fields := strings.Fields(ln)
						if len(fields) >= 2 {
							if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
								memAvailKB = kb
							}
						}
						break
					}
				}
			}
		}
		memAvailGB := memAvailKB / (1024 * 1024)
		ramBound := int(memAvailGB / 4)
		if ramBound < 1 {
			ramBound = 1
		}
		if derived > ramBound {
			derived = ramBound
		}
		if derived > 8 {
			derived = 8
		}
		result.HeavyConcurrency = derived
	}
	return result
}

// showUsage (styled)
func showUsage() {
	fmt.Println()
	// Styled CLI help matching the TUI theme
	logoTop := " █▀▀ ▄▀█ █▀█ █▀█"
	logoBottom := fmt.Sprintf(" █▄█ █▀█ █▀▄ █▀▀  v%s", version)
	// Pad lines to equal width and render left-aligned to avoid odd spacing
	if len(logoTop) < len(logoBottom) {
		logoTop += strings.Repeat(" ", len(logoBottom)-len(logoTop))
	} else if len(logoBottom) < len(logoTop) {
		logoBottom += strings.Repeat(" ", len(logoTop)-len(logoBottom))
	}
	fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Render(logoTop + "\n" + logoBottom))
	fmt.Println()

	// Usage
	fmt.Println(subHeaderStyle.Render("USAGE"))
	fmt.Println(infoStyle.Render(wrapTextWithIndent("  garp ", "[--code] [--distance N] [--heavy-concurrency N] [--workers N] [--file-timeout-binary N] <word1> <word2> ... [--not <exclude1> <exclude2> ...]", 100)))
	fmt.Println()

	// Flags
	fmt.Println(subHeaderStyle.Render("FLAGS"))
	fmt.Println(infoStyle.Render("  --code                  Include code files in the search"))
	fmt.Println(infoStyle.Render("  --distance N            Proximity window in characters (default 5000)"))
	fmt.Println(infoStyle.Render("  --heavy-concurrency N   Concurrent heavy extractions (auto if omitted)"))
	fmt.Println(infoStyle.Render("  --workers N             Stage 2 text filter workers (default 2)"))
	fmt.Println(infoStyle.Render("  --file-timeout-binary N Timeout in ms for binary extraction (default 1000)"))
	fmt.Println(infoStyle.Render("  --not ...               Tokens after this are exclusions;"))
	fmt.Println(infoStyle.Render("                          extensions starting with '.' exclude types; others exclude words"))
	fmt.Println(infoStyle.Render("  --help, -h              Show help"))
	fmt.Println(infoStyle.Render("  --version, -v           Show version"))
	fmt.Println()

	// Examples
	fmt.Println(subHeaderStyle.Render("EXAMPLES"))
	fmt.Println(infoStyle.Render("  garp contract payment agreement"))
	fmt.Println(infoStyle.Render("  garp contract payment agreement --distance 200"))
	fmt.Println(infoStyle.Render("  garp mutex changed --code"))
	fmt.Println(infoStyle.Render("  garp bank wire update --not .txt test"))
	fmt.Println()
}

// showVersion
func showVersion() {
	// successStyle is provided in tui.go (same package).
	fmt.Println(successStyle.Render("garp v" + version))
}

// Run parses CLI arguments and starts the TUI. Returns a process exit code.
func Run() int {
	// Parse args
	args := parseArguments(os.Args[1:])
	if len(args.SearchWords) == 0 {
		showUsage()
		return 1
	}

	// Seed model for TUI
	m := model{
		results:           []search.SearchResult{},
		currentPage:       0,
		pageSize:          1,
		totalPages:        0,
		searchTime:        0,
		quitting:          false,
		loading:           true,
		width:             0,
		height:            0,
		searchWords:       args.SearchWords,
		excludeWords:      args.ExcludeWords,
		includeCode:       args.IncludeCode,
		distance:          args.Distance,
		heavyConcurrency:  args.HeavyConcurrency,
		fileTimeoutBinary: args.FileTimeoutBinary,
		filterWorkers:     args.FilterWorkers,
		confirmSelected:   "yes",
		memUsageText:      "",
		progressText:      "",
	}

	// Start TUI
	startWall = time.Now()
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Println("Error:", err)
		return 1
	}
	return 0
}
