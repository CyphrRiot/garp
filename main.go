package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"find-words/config"
	"find-words/search"
)

const (
	RED    = "\033[0;31m"
	GREEN  = "\033[0;32m"
	YELLOW = "\033[1;33m"
	BLUE   = "\033[0;34m"
	GRAY   = "\033[0;90m"
	NC     = "\033[0m"
	BOLD   = "\033[1m"
)

func main() {
	// Check if ripgrep is available
	if !isRipgrepAvailable() {
		fmt.Printf("%sError: ripgrep (rg) is required but not installed%s\n", RED, NC)
		fmt.Printf("%sInstall with: sudo pacman -S ripgrep%s\n", YELLOW, NC)
		os.Exit(1)
	}

	// Parse command line arguments
	args := parseArguments(os.Args[1:])
	if args == nil {
		showUsage()
		os.Exit(1)
	}

	// Validate inputs
	if len(args.SearchWords) == 0 {
		fmt.Printf("%sError: No search words provided%s\n", RED, NC)
		os.Exit(1)
	}

	// Build file types for ripgrep
	fileTypes := config.BuildRipgrepFileTypes(args.IncludeCode)

	// Show search information
	showSearchInfo(args)

	// Get file count estimate
	fileCount, err := search.GetDocumentFileCount(fileTypes)
	if err != nil {
		fmt.Printf("%sWarning: Could not estimate file count: %v%s\n", YELLOW, err, NC)
		fileCount = 0
	}

	if fileCount > 0 {
		fmt.Printf("%sDocument files to search:%s %s%s%s\n", 
			BOLD, NC, YELLOW, formatNumber(fileCount), NC)
		fmt.Printf("%sEstimated time:%s %s%s%s\n", 
			BOLD, NC, YELLOW, config.GetEstimatedSearchTime(fileCount), NC)
	}
	fmt.Println()

	// Create and execute search
	engine := search.NewSearchEngine(args.SearchWords, args.ExcludeWords, fileTypes, args.IncludeCode)
	results, err := engine.Execute()
	if err != nil {
		fmt.Printf("%sError during search: %v%s\n", RED, err, NC)
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Printf("%sNo matching files found.%s\n", YELLOW, NC)
		return
	}

	// Display results
	fmt.Printf("\n%s%sFound %s files containing all words:%s\n\n", 
		BOLD, GREEN, formatNumber(len(results)), NC)

	displayResults(results)
}

// Arguments represents parsed command line arguments
type Arguments struct {
	SearchWords  []string
	ExcludeWords []string
	IncludeCode  bool
}

// parseArguments parses command line arguments
func parseArguments(args []string) *Arguments {
	if len(args) == 0 {
		return nil
	}

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

// showUsage displays the usage information
func showUsage() {
	fmt.Printf("%s%sfind-words%s - Multi-word file search tool (ripgrep-based)\n", BOLD, BLUE, NC)
	fmt.Println()
	fmt.Printf("%sUSAGE:%s\n", BOLD, NC)
	fmt.Printf("  find-words %sword1 word2 word3%s [...]\n", YELLOW, NC)
	fmt.Printf("  find-words %s--code%s word1 word2 [...]\n", YELLOW, NC)
	fmt.Printf("  find-words word1 word2 %s--not%s %sexcludeword%s [...]\n", RED, NC, YELLOW, NC)
	fmt.Println()
	fmt.Printf("%sOPTIONS:%s\n", BOLD, NC)
	fmt.Printf("  %s--code%s    Include programming/code files (.js, .py, .sql, etc.)\n", YELLOW, NC)
	fmt.Printf("  %s--not%s     Exclude files containing the following words\n", RED, NC)
	fmt.Println()
	fmt.Printf("%sEXAMPLES:%s\n", BOLD, NC)
	fmt.Printf("  find-words contract payment\n")
	fmt.Printf("  find-words --code function database\n")
	fmt.Printf("  find-words chris incentive --not test demo\n")
	fmt.Printf("  find-words ethereum --not scam --not fake\n")
	fmt.Println()
}

// showSearchInfo displays the search configuration
func showSearchInfo(args *Arguments) {
	fmt.Printf("%s%s🔍 Multi-Word Search (ripgrep)%s\n", BOLD, BLUE, NC)
	fmt.Printf("%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", GRAY, NC)

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
		fmt.Printf("%sExclude count:%s %s%d%s\n", BOLD, NC, YELLOW, len(args.ExcludeWords), NC)
	}

	// Show mode
	if args.IncludeCode {
		fmt.Printf("%sMode:%s %sDOCUMENTS + CODE FILES%s\n", BOLD, NC, BLUE, NC)
	} else {
		fmt.Printf("%sMode:%s %sDOCUMENTS ONLY (use --code for programming files)%s\n", BOLD, NC, BLUE, NC)
	}
	fmt.Println()
}

// displayResults shows the search results with interactive paging
func displayResults(results []search.SearchResult) {
	reader := bufio.NewReader(os.Stdin)

	for i, result := range results {
		absPath := search.GetAbsolutePath(result.FilePath)
		
		fmt.Printf("%s📄 File %d/%d:%s %s%s%s\n", 
			BOLD, i+1, len(results), NC, GREEN, absPath, NC)
		fmt.Printf("    🔗 %sfile://%s%s\n", BLUE, absPath, NC)

		// Show file size warning for large files
		if result.FileSize > 50*1024*1024 {
			fmt.Printf("    %s⚠️  Large file (%s) - limiting search scope%s\n", 
				YELLOW, search.FormatFileSize(result.FileSize), NC)
		} else if result.FileSize > 10*1024*1024 {
			fmt.Printf("    %s📊 Medium file (%s) - limiting search scope%s\n", 
				YELLOW, search.FormatFileSize(result.FileSize), NC)
		}

		fmt.Printf("    📋 %sContent matches:%s\n", BOLD, NC)

		// Show excerpts
		if len(result.Excerpts) == 0 {
			fmt.Printf("    %sNo readable excerpts found%s\n", GRAY, NC)
		} else {
			for _, excerpt := range result.Excerpts {
				if len(excerpt) > 0 {
					fmt.Printf("    %s\n", excerpt)
				}
			}
		}

		fmt.Println()

		// Interactive paging
		if i < len(results)-1 {
			fmt.Printf("%s%s[Press ENTER for next file, 'q' + ENTER to quit]%s", BOLD, YELLOW, NC)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			if input == "q" {
				fmt.Printf("%sSearch stopped by user.%s\n", YELLOW, NC)
				break
			}
			fmt.Println()
		}
	}
}

// isRipgrepAvailable checks if ripgrep is installed
func isRipgrepAvailable() bool {
	_, err := exec.LookPath("rg")
	return err == nil
}

// formatNumber formats a number with thousands separators
func formatNumber(n int) string {
	str := fmt.Sprintf("%d", n)
	if len(str) <= 3 {
		return str
	}

	var result strings.Builder
	for i, digit := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result.WriteString(",")
		}
		result.WriteRune(digit)
	}

	return result.String()
}
