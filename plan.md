# garp Plan — Fast, Truthful, and Reliable (v0.4)

This plan reflects the current state of garp v0.4, recent improvements, and the path forward. We operate with one change at a time and validate with `make install`.

# NOW

Plan updated with clear priorities and next steps:

- Stage 2 (FilterCandidates) now parallelized with a small worker pool via --workers; rarest-term checks pushed earlier; heavy extraction remains strictly gated.
- .eml/.msg latency tightened with 256 KB streaming prefilters and strict timeouts; basic latency metrics added for tuning.
- Conservative prefilters added for DOCX/ODT (ZIP+XML) and DOC (OLE sniff); undecided prefilters proceed, only decided negatives skip.
- Next focus: safe PDF reintroduction, tests/benchmarks, and documentation/guardrails.

Ready for the next single change. Options aligned with the plan:

1. Safely reintroduce PDFs with presence-only and distance-window scanning under strict caps/timeouts.
2. Add tests/benchmarks and latency sampling; record perf baselines.
3. Document flags (--workers, --file-timeout-binary, --distance) and reiterate “no timeout-induced skipping.”

## Current Status (v0.4)

**Core Features:**

- Unordered AND matching within 5000-character proximity window
- Whole-word, case-insensitive, plural-aware (e.g., "explanation" matches "explanations")
- CLI: `--distance N` to override proximity
- PDF handling conservative/guarded; safe reintroduction prioritized next

**Discovery (Stage 1):**

- Single-threaded, rate-limited file opens (2ms sleep)
- Zero-allocation ASCII scanner for first term
- Minimal overlap, explicit closes, FADV_DONTNEED hints
- Includes .eml/.msg/.mbox as candidates (no prefilter drops)

**Processing (Stage 2):**

- Text: Streaming prefilters for second term and rarest terms (length heuristic); parallel worker pool for text filtering (--workers)
- Binary: EML/MBOX parsed from bytes; MSG UTF-16 decode with fallbacks
- Prefilters: Plural-aware streaming for all-words presence; conservative prefilters for DOC/DOCX/ODT
- Heavy extraction: 1-slot semaphore, 1000ms timeout per file; timeouts cancel work but never classify as non-match (no timeout-induced skipping; undecided continues to extraction)
- Body-only for EML/MSG (no attachments)
- Exclude-words checked post-extraction

**Results:**

- Email metadata (Date/Subject) for .eml/.msg
- Match-first excerpts with punctuation/email boundaries, paragraph fallback
- Plural-aware dedupe and highlighting
- Precomputed excerpts only (no large retained content)

**TUI:**

- ASCII "GARP" logo with version displayed in the header
- Target line listing supported document extensions (truthful)
- Engine line: Concurrency: N • Go Heap • Resident • CPU (live)
- Elapsed time: “Searching” while loading; “Search” after completion
- Search terms line (quoted, with excludes noted)
- Progress line with stage label: "⏳ Discovery [count/total]: path" or "⏳ Processing [count/total]: path"
- Tokyo Night theme

**What's Working Well:**

- Fast, stable discovery (no FD issues, controlled cache)
- Faster multi-term searches via plural-aware prefilters
- Bounded .eml/.msg extraction with early exits
- Precise, readable results with metadata

## Problems to Fix Next

- P1 — Safe PDF reintroduction
    - Presence-only and distance-window scanning; strict page/time caps; extraction only for excerpts on positives
- P2 — Tests, benchmarks, and sampling
    - TUI keybinding/layout tests; scanner/prefilter benchmarks; latency sampling; record perf baselines
- P3 — Excerpt/UX polish
    - Ensure stitched, sentence-aware all-terms excerpt is first; keep excerpt window tied to distance; verify scroll behavior
- P4 — Guardrails and flags
    - Document --workers, --file-timeout-binary, --distance; reiterate “no timeout-induced skipping”

## Design Principles

- One change at a time; validate with `make install`
- Fast, targeted checks; bounded reads; drop cache after scans
- Heavy ops under semaphore + timeouts; never block UI

## Completed Steps

1. Heavy semaphore + timeouts (DONE)
2. Body-only extraction (DONE)
3. Rarest-terms prefilter (DONE — text/binary with length heuristic)
4. Engine refactoring (DONE — Execute split; ConcurrencyManager added)
5. Clear stage labeling in code and logs (DONE)
6. TUI header parity restored (ASCII logo + version), truthful target/engine lines, combined elapsed header (DONE)
7. Metrics stabilized (fixed-width memory/CPU); version wired via app.version (DONE)
8. Footer/layout stabilized; removed extra bottom blank lines; consistent footer color (DONE)
9. Vertical scrolling inside results box (Up/Down/PgUp/PgDn); viewport stable (DONE)
10. Excerpts: single-line wrapped labels; email quote cleanup; sentence-stitched all-terms excerpt first; window tied to distance (DONE)
11. .MSG extraction via OLE streams; subject/body + PR_HTML fallback; UTF-16 best-effort; ASCII salvage (DONE)
12. Matching aligned with cleaned content to avoid excerpt mismatches (DONE)
13. Stage 2 worker pool for text filtering via --workers; heavy extraction remains gated (DONE)
14. Prefilter gating semantics fixed: undecided proceeds; only decided false skips (DONE)
15. DOCX/ODT prefilters (ZIP+XML) and DOC prefilter (OLE sniff) added (DONE)
16. .EML/.MSG latency metrics added; 256 KB prefilter caps and strict timeouts (DONE)

## Codebase Optimization & Modularization (In Progress)

Goal: Make the codebase faster to build, easier to test, and safer to evolve by modularizing major components and tightening interfaces across all .go files.

- Scope
    - All .go files: main.go, engine.go, discovery, filters/prefilters, extractors (eml/mbox/msg), results/excerpts, TUI, I/O utilities
    - Package layout: root main.go calls app.Run(); app/ contains CLI and TUI; search/ and config/ remain

- In Progress
    - main.go: now a tiny wrapper; CLI/TUI moved to app/; centralized Options/Config; context propagation; dependency injection of Engine and services

- Planned Tasks
    - Interfaces and boundaries
        - Define Engine interface exposing DiscoverCandidates, FilterCandidates, ExtractAndBuildResults
        - Extract Extractor interface (EML/MBOX/MSG implementations) with timeouts and context
        - Prefilter interface for streaming checks (plural-aware, rarest-terms selection)
        - Discovery scanner as its own package with rate limiter injectable
    - Packages
        - internal/engine, internal/discovery, internal/prefilter, internal/extract, internal/results, internal/ui, internal/sys (FADV, hints)
    - main.go
        - Command setup: flags, guardrails, defaults
        - Wire concurrency manager, loggers, and metrics
        - Clean shutdown: context cancellation, drain workers
    - Quality and performance
        - Benchmarks for scanners, prefilters, and extractors
        - Tracing/pprof toggles via flags; minimal overhead when off
        - Static analysis: go vet, staticcheck; consistent errors with wrapping/sentinels
    - Build and targeting
        - Optional build tags (e.g., pdf) and safe defaults
        - Reusable package docs and diagrams

- Milestone Acceptance
    - main.go reduced to orchestration only; pkg boundaries enforced
    - Unit tests for each package; benchmark baselines recorded
    - No regressions in latency or memory; stages remain truthful in UI

## Next Steps (In Order)

1. Safe PDF reintroduction
    - Presence-only and distance-window page scanning; strict caps/timeouts; extraction only for excerpts
2. Tests and benchmarks
    - TUI keybinding/layout tests; scanner/prefilter benchmarks; latency sampling; perf baselines
3. Guardrails and flags
    - Document --workers, --file-timeout-binary, --distance; reiterate “no timeout-induced skipping”
4. Excerpt/UX polish
    - Ensure stitched, sentence-aware all-terms excerpt is first; keep excerpt window tied to distance; verify scroll behavior
5. Documentation
    - README/Architecture reflect current behavior, header layout, progress lines, and safety policies

## Done Criteria

- Header truthful and stable; ASCII logo visible; fixed-width metrics
- UX: Quit works (q/ctrl+c); viewport fixed; no bottom whitespace; scrolling smooth
- Performance: Faster Stage 2; bounded .eml/.msg latency; no hangs; timeouts never mark non-match
- Accuracy: All-terms excerpt shown first; plural-aware; clean sentence-based stitching with metadata
- Observability: Tests for keybindings/layout; prefilter benchmarks and latency sampling recorded

## Summary

Where we are (v0.4):

- TUI restored and stable (ASCII logo, truthful target/engine, blue footer); no layout jumps; vertical scrolling in box
- Metrics fixed-width; elapsed frozen after completion; combined header shows “Searched • Matched”
- Excerpts improved: sentence-stitched all-terms excerpt first; window tied to distance; quote cleanup
- Matching aligned with cleaned content to reduce confusing matches
- .MSG parsing upgraded (OLE streams + UTF-16); .EML path stable with gates
- Stage 2 parallelized with a small worker pool (--workers); email latency tightened with capped prefilters and metrics

What’s left (focus on optimization):

- P1: Speed up Stage 2 text filtering with a small worker pool; push rarest-term checks up front
- P2: Reduce .eml/.msg latency further with small caps and strict gates/timeouts
- P3: Add DOCX/ODT conservative prefilters and broader streaming optimizations
- P4: Safely reintroduce PDFs with presence-only and distance-window checks

Next steps (one at a time, in order):

1. Reintroduce PDFs safely (presence-only + distance-window; excerpt-only extraction; strict caps/timeouts)
2. Add tests/benchmarks and latency sampling; record perf baselines
3. Document flags and guardrails (--workers, --file-timeout-binary, --distance); reiterate safety policies
4. Excerpt/UX polish (verify scroll behavior and top excerpt correctness)

One change at a time. Verify with `make install`.

# Next Steps

Two things to tackle now:

A) Restore the full header parity you want (logo/version/target/engine/elapsed/progress/terms).

- Single change proposal: Reintroduce the exact header composition from your previous main.go into app/tui.go’s View, preserving the Tokyo Night styles.
    - Add ASCII GARP logo + version at the top (two-line banner).
    - Add Target line via config.GetFileTypeDescription(includeCode) and keep it truthful.
    - Keep Engine line with live Concurrency, Go Heap, Resident, CPU (we already restored memUsageTick; we’ll keep the styling).
    - Keep elapsed time label with “Searching” vs “Search.”
    - Ensure stage-labeled progress line uses “⏳ Discovery/Processing [count/total]: path”.
    - Keep search terms on its own header line.
    - Use the same default width/height fallback we added.
- System: Code Repo
- File: app/tui.go
- I will transplant the header parts without touching search/extraction logic.

B) Proposed next optimization steps (after header parity), in order:

1. Optional small filter worker pool for Stage 2
    - Add a flag: --filter-workers N (default 2)
    - Implement a goroutine pool to evaluate FilterCandidates in parallel for text files (all prefilters and checks apply), maintaining heavy extraction concurrency with the existing semaphore/timeout.
    - Benefit: Parallelizes compute-heavy checks for large sets of candidate text files; leaves binary extraction bounded.
2. Conservative binary prefilters for DOCX/ODT
    - Add a minimal zip sniff prefilter that scans the ZIP central directory names (no large reads), then streams “word/document.xml” or “content.xml” in a capped window using the same plural-aware whole-word logic. If conclusively absent, skip; otherwise proceed.
    - Benefit: Avoids many full unzip+parse operations for DOCX/ODT when terms simply aren’t present.
3. PDF reintroduction with strict guardrails
    - Use existing PDFContainsAllWordsNoDistancePath and PDFHasAllWordsWithinDistanceNoExtractPath as prefilters.
    - Extraction remains optional and only to build excerpts for matched results; undecided cases proceed without classifying as non-match due to timeouts.
    - Keep single heavy slot and a conservative page/time cap for prefilter scanning.
4. README updates and polish
    - Document the flags, concurrency behavior, header layout, and the “no timeout-induced skipping” policy.
