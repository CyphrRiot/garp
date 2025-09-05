package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"find-words/config"
	"find-words/search"
)

// Color codes for terminal output
const (
	RED    = "\033[31m"
	GREEN  = "\033[32m"
	YELLOW = "\033[33m"
	BLUE   = "\033[34m"
	GRAY   = "\033[90m"
	BOLD   = "\033[1m"
	NC     = "\033[0m" // No Color
)

// getTerminalWidth returns the terminal width, defaulting to 80 if unable to detect
func getTerminalWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return 80 // Default fallback width
	}
	return width
}

// createSeparator creates a separator line that fits the terminal width
func createSeparator() string {
	width := getTerminalWidth()
	if width > 120 {
		width = 120 // Maximum reasonable width
	}
	return strings.Repeat("━", width)
}

// Arguments holds parsed command line arguments
type Arguments struct {
	SearchWords  []string
	ExcludeWords []string
	IncludeCode  bool
}

func main() {
	// Parse arguments
	args := parseArguments(os.Args[1:])

	// Validate arguments
	if len(args.SearchWords) == 0 {
		showUsage()
		os.Exit(1)
	}

	// Show search information
	showSearchInfo(args)

	// Create search engine with pure Go implementation
	searchEngine := search.NewSearchEngine(
		args.SearchWords,
		args.ExcludeWords,
		config.DocumentTypes,
		config.CodeTypes,
		args.IncludeCode,
	)
	defer searchEngine.Close()

	// Execute the search
	startTime := time.Now()
	results, err := searchEngine.Execute()
	if err != nil {
		fmt.Printf("%sError: %v%s\n", RED, err, NC)
		os.Exit(1)
	}

	totalTime := time.Since(startTime)

	if len(results) == 0 {
		fmt.Printf("\n%s🔍 No files found containing all search terms%s\n", YELLOW, NC)
		fmt.Printf("Try:\n")
		fmt.Printf("  • Using fewer search terms\n")
		fmt.Printf("  • Removing exclude words (--not)\n")
		fmt.Printf("  • Adding --code flag for programming files\n")
		return
	}

	// Show interactive results
	fmt.Printf("\n%s%s📋 Found %d files with matches%s\n", BOLD, GREEN, len(results), NC)
	fmt.Printf("%s%s%s\n", GRAY, createSeparator(), NC)

	showInteractiveResults(results, totalTime)
}

// parseArguments parses command line arguments
func parseArguments(args []string) *Arguments {
	result := &Arguments{
		SearchWords:  make([]string, 0),
		ExcludeWords: make([]string, 0),
		IncludeCode:  false,
	}

	parsingExcludes := false

	for _, arg := range args {
		switch arg {
		case "--code":
			result.IncludeCode = true
		case "--not":
			parsingExcludes = true
		case "--help", "-h":
			showUsage()
			os.Exit(0)
		case "--version", "-v":
			showVersion()
			os.Exit(0)
		default:
			if parsingExcludes {
				result.ExcludeWords = append(result.ExcludeWords, arg)
			} else {
				result.SearchWords = append(result.SearchWords, arg)
			}
		}
	}

	return result
}

// showUsage displays usage information
func showUsage() {
	fmt.Printf("%s%sfind-words%s - High-Performance Document Search Tool (Pure Go)\n", BOLD, BLUE, NC)
	fmt.Println()
	fmt.Printf("%sUSAGE:%s\n", BOLD, NC)
	fmt.Printf("  find-words %sword1 word2 word3%s [...]\n", YELLOW, NC)
	fmt.Printf("  find-words %s--code%s word1 word2 [...]\n", YELLOW, NC)
	fmt.Printf("  find-words word1 word2 %s--not%s %sexcludeword%s [...]\n", RED, NC, YELLOW, NC)
	fmt.Println()
	fmt.Printf("%sOPTIONS:%s\n", BOLD, NC)
	fmt.Printf("  %s--code%s    Include programming/code files (.js, .py, .sql, etc.)\n", YELLOW, NC)
	fmt.Printf("  %s--not%s     Exclude files containing the following words\n", RED, NC)
	fmt.Printf("  %s--help%s    Show this help message\n", YELLOW, NC)
	fmt.Printf("  %s--version%s Show version information\n", YELLOW, NC)
	fmt.Println()
	fmt.Printf("%sEXAMPLES:%s\n", BOLD, NC)
	fmt.Printf("  find-words contract payment agreement\n")
	fmt.Printf("  find-words --code function database --not test\n")
	fmt.Printf("  find-words bitcoin ethereum --not scam --not demo\n")
	fmt.Println()
	fmt.Printf("%sPERFORMANCE:%s\n", BOLD, NC)
	fmt.Printf("  • %s100%% Pure Go%s - No external dependencies\n", GREEN, NC)
	fmt.Printf("  • %sParallel Processing%s - Multi-core CPU utilization\n", GREEN, NC)
	fmt.Printf("  • %sMemory Optimized%s - Efficient for large file sets\n", GREEN, NC)
	fmt.Printf("  • %sSmart Filtering%s - Advanced file type detection\n", GREEN, NC)
}

// showVersion displays version information
func showVersion() {
	fmt.Printf("%sfind-words%s v2.0.0\n", BOLD, NC)
	fmt.Printf("High-Performance Document Search Tool\n")
	fmt.Printf("Pure Go Implementation - No Dependencies\n")
	fmt.Printf("Copyright © 2024\n")
}

// showSearchInfo displays search configuration
func showSearchInfo(args *Arguments) {
	fmt.Printf("%s%s🚀 High-Performance Multi-Word Search%s\n", BOLD, BLUE, NC)
	fmt.Printf("%s%s%s\n", GRAY, createSeparator(), NC)

	// Show search terms
	quotedWords := make([]string, len(args.SearchWords))
	for i, word := range args.SearchWords {
		quotedWords[i] = fmt.Sprintf("\"%s\"", word)
	}
	fmt.Printf("%sSearching for:%s %s%s%s\n", BOLD, NC, GREEN, strings.Join(quotedWords, " "), NC)
	fmt.Printf("%sWord count:%s %s%d%s\n", BOLD, NC, YELLOW, len(args.SearchWords), NC)

	// Show exclude terms if any
	if len(args.ExcludeWords) > 0 {
		quotedExcludes := make([]string, len(args.ExcludeWords))
		for i, word := range args.ExcludeWords {
			quotedExcludes[i] = fmt.Sprintf("\"%s\"", word)
		}
		fmt.Printf("%sExcluding files with:%s %s%s%s\n", BOLD, NC, RED, strings.Join(quotedExcludes, " "), NC)
	}

	// Show file type configuration
	fileTypeDesc := config.GetFileTypeDescription(args.IncludeCode)
	fmt.Printf("%sTarget files:%s %s%s%s\n", BOLD, NC, YELLOW, fileTypeDesc, NC)

	// Show performance info
	fmt.Printf("%sEngine:%s %sPure Go - Parallel Processing%s\n", BOLD, NC, GREEN, NC)
	fmt.Printf("%sMemory usage:%s %sOptimized for large datasets%s\n", BOLD, NC, GREEN, NC)

	fmt.Println()
}

// showInteractiveResults displays search results with pagination
func showInteractiveResults(results []search.SearchResult, searchTime time.Duration) {
	reader := bufio.NewReader(os.Stdin)

	for i, result := range results {
		// Clear screen and show header
		fmt.Printf("\n%s📄 File %d/%d: %s%s\n", BLUE, i+1, len(results), result.FilePath, NC)

		// Show file info
		absolutePath := search.GetAbsolutePath(result.FilePath)
		fmt.Printf("    %s🔗 file://%s%s\n", GRAY, absolutePath, NC)

		if result.FileSize > 0 {
			fmt.Printf("    %s📦 Size: %s%s\n", GRAY, search.FormatFileSize(result.FileSize), NC)
		}

		// Show content excerpts
		if len(result.Excerpts) > 0 {
			fmt.Printf("    %s📋 Content matches:%s\n", GRAY, NC)
			for _, excerpt := range result.Excerpts {
				// Indent and show excerpt
				lines := strings.Split(excerpt, "\n")
				for _, line := range lines {
					if strings.TrimSpace(line) != "" {
						fmt.Printf("    %s\n", line)
					}
				}
			}
		} else {
			fmt.Printf("    %s📋 File contains all search terms%s\n", GRAY, NC)
		}

		// Show navigation prompt
		fmt.Printf("\n%s[Press ENTER for next file", GRAY)
		if i < len(results)-1 {
			fmt.Printf(", 's' + ENTER to skip remaining")
		}
		fmt.Printf(", 'q' + ENTER to quit]%s ", NC)

		// Read user input
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))

		switch input {
		case "q", "quit", "exit":
			fmt.Printf("\n%s✨ Search session ended%s\n", YELLOW, NC)
			return
		case "s", "skip":
			fmt.Printf("\n%s⏭️  Skipping remaining results%s\n", YELLOW, NC)
			return
		}
	}

	// All results shown
	fmt.Printf("\n%s✅ All results displayed%s\n", GREEN, NC)
	fmt.Printf("%s📊 Search completed in %.2f seconds%s\n", GRAY, searchTime.Seconds(), NC)
	fmt.Printf("%s🎉 Found %d matching files%s\n", GREEN, len(results), NC)
}
