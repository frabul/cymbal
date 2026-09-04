package index

import (
	"sort"
	"strings"
)

// rankFetchWindow returns how many rows to over-fetch before ranking.
//
// exactQuery=true: fetch all matching rows (no cap) so the ranking window
// is never truncated before canonical scoring; exact queries share one symbol
// name so the extra rows are bounded by definition count, not corpus size.
//
// exactQuery=false (FTS): cap at min(limit*5, 500) — different symbol names
// mix in FTS results so an unbounded fetch would be expensive.
func rankFetchWindow(userLimit int, exactQuery bool) int {
	if exactQuery {
		// 0 means no LIMIT in the SQL query for exact matches.
		return 0
	}
	if userLimit <= 0 {
		return 500
	}
	w := userLimit * 5
	if w > 500 {
		w = 500
	}
	return w
}

// RankSymbols sorts a slice of SymbolResults so the canonical definition
// appears first. Identical logic is used by both the store (over-fetch window)
// and the cmd layer so they are always consistent.
func RankSymbols(results []SymbolResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return SymbolScore(results[i]) > SymbolScore(results[j])
	})
}

// SymbolScore returns a heuristic relevance score for a symbol result.
// Higher is better / more canonical.
func SymbolScore(r SymbolResult) int {
	score := 0
	p := strings.ToLower(r.RelPath)

	// Kind priority.
	switch r.Kind {
	case "class", "struct", "union", "interface", "type":
		score += 60
	case "function":
		score += 50
	case "method":
		score += 40
	case "enum":
		score += 30
	case "constructor":
		score += 20
	case "impl":
		score += 15
	case "variable", "constant":
		score += 10
	}

	// Penalise test files. Shares the single ClassifyPath definition of "test"
	// so the two never drift, and so top-level test dirs (e.g. `tests/foo.py`,
	// `spec/x.rb`) — which the old "/test/"-anchored list missed — are caught.
	if ClassifyPath(r.RelPath) == PathClassTest {
		score -= 80
	}
	// Penalise test-support / playground / example / fixture paths. These are
	// ClassifyPath "unknown" (kept by --no-tests) but still non-canonical for
	// ranking, so they keep their own penalty here.
	for _, seg := range []string{
		"/testing/", "/testutil/", "/testutils/",
		"/playground/", "/example/", "/examples/",
		"/demo/", "/demos/", "/sample/", "/samples/",
		"/fixture/", "/fixtures/",
	} {
		if strings.Contains(p, seg) {
			score -= 70
			break
		}
	}
	// Penalise doc paths.
	for _, seg := range []string{
		"/docs/", "/docs_src/", "/doc/", "/documentation/",
	} {
		if strings.Contains(p, seg) {
			score -= 60
			break
		}
	}
	// Penalise vendored / third-party paths.
	for _, seg := range []string{
		"/vendor/", "/node_modules/", "/third_party/",
		"/external/", "/deps/",
	} {
		if strings.Contains(p, seg) {
			score -= 90
			break
		}
	}
	// Penalise mirror / alternate build trees (e.g. guava android/).
	for _, prefix := range []string{
		"android/", "guava-gwt/",
	} {
		if strings.HasPrefix(p, prefix) || strings.Contains(p, "/"+prefix) {
			score -= 50
			break
		}
	}

	// Penalise generated code. Patterns are conservative to avoid false-positives
	// on files like generator.go or generate_test.go.
	for _, seg := range []string{
		".pb.go", "_pb2.py", "_pb2_grpc.py",
		"_generated.go", "_gen.go", ".gen.go",
		".generated.ts", ".generated.js", ".gen.ts",
		"__generated__",
		"_pb.d.ts", "_grpc.pb.go",
		".g.dart",
	} {
		if strings.HasSuffix(p, seg) || strings.Contains(p, seg+"/") {
			score -= 70
			break
		}
	}
	// Also catch files with a "// Code generated" or "DO NOT EDIT" header
	// via the name pattern; actual content inspection is deferred to P3.
	for _, seg := range []string{
		"/generated/", "/gen/",
	} {
		if strings.Contains(p, seg) {
			score -= 50
			break
		}
	}

	// Prefer well-known source roots.
	for _, seg := range []string{
		"/src/", "/pkg/", "/lib/", "/crates/",
		"/packages/", "/internal/", "/cmd/",
	} {
		if strings.Contains(p, seg) {
			score += 15
			break
		}
	}

	// Shallower paths are more likely canonical.
	score -= strings.Count(r.RelPath, "/") * 3
	// Shorter path as minor tiebreaker.
	score -= len(r.RelPath) / 10

	return score
}
