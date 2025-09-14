package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	benchFound   bool
	benchDecided bool
)

func BenchmarkStreamContainsAllWordsDecidedWithCap_Hit(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "hit.txt")

	// Build a ~1MB file with both terms appearing early (well within the cap)
	const targetSize = 1 << 20 // ~1MB
	var sb strings.Builder
	sb.Grow(targetSize + 128)
	// Put the target words near the start so the prefilter can find them quickly
	sb.WriteString("This is a benchmark file containing motor and vehicles early. ")
	// Fill the rest with benign text
	fill := "lorem ipsum dolor sit amet consectetur adipiscing elit "
	for sb.Len() < targetSize {
		sb.WriteString(fill)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		b.Fatalf("failed to write hit test file: %v", err)
	}

	words := []string{"motor", "vehicles"}
	const capBytes = 256 * 1024 // 256KB

	// Sanity check once before measuring
	found, decided := StreamContainsAllWordsDecidedWithCap(path, words, capBytes)
	if !decided || !found {
		b.Fatalf("sanity check failed for hit case: found=%v decided=%v", found, decided)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchFound, benchDecided = StreamContainsAllWordsDecidedWithCap(path, words, capBytes)
	}
	_ = benchFound
	_ = benchDecided
}

func BenchmarkStreamContainsAllWordsDecidedWithCap_Miss(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "miss.txt")

	// Build a ~1MB file that intentionally does not include the second term.
	const targetSize = 1 << 20 // ~1MB
	var sb strings.Builder
	sb.Grow(targetSize + 128)
	// Include only the first word "motor", omit "vehicles"
	sb.WriteString("This is a benchmark file containing motor but not the other required term. ")
	fill := "lorem ipsum dolor sit amet consectetur adipiscing elit "
	for sb.Len() < targetSize {
		sb.WriteString(fill)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		b.Fatalf("failed to write miss test file: %v", err)
	}

	words := []string{"motor", "vehicles"}
	const capBytes = 2 * 1024 * 1024 // 2MB

	// Sanity check once before measuring
	found, decided := StreamContainsAllWordsDecidedWithCap(path, words, capBytes)
	// For a definitive miss we expect decided=true (EOF) and found=false
	if !decided || found {
		b.Fatalf("sanity check failed for miss case: found=%v decided=%v", found, decided)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchFound, benchDecided = StreamContainsAllWordsDecidedWithCap(path, words, capBytes)
	}
	_ = benchFound
	_ = benchDecided
}
