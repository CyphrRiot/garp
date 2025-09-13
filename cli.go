package main

import (
	"fmt"
	"os"
	"strconv"
)

var version = "0.2"

// Arguments for CLI flags (used to seed TUI)
type Arguments struct {
	SearchWords       []string
	ExcludeWords      []string
	IncludeCode       bool
	Distance          int
	HeavyConcurrency  int
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
		FileTimeoutBinary: 1000,
	}

	parsingExcludes := false
	expectDistance := false
	expectHeavy := false
	expectTimeout := false

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

	return result
}

// showUsage (basic)
func showUsage() {
	fmt.Println(headerStyle.Render("garp - High-Performance Document Search Tool (Pure Go)"))
	fmt.Println()
	fmt.Printf("%sUSAGE:%s\n", subHeaderStyle.Render("USAGE:"), "")
	fmt.Printf("  garp [--code] [--distance N] [--heavy-concurrency N] [--file-timeout-binary N] word1 word2 [...]\n")
	fmt.Printf("  garp [--code] [--distance N] [--heavy-concurrency N] [--file-timeout-binary N] word1 word2 --not excludeword [...]\n")
	fmt.Println()
}

// showVersion
func showVersion() {
	fmt.Println(successStyle.Render("garp v" + version))
}
