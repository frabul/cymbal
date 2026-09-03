package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCERefFixture creates a C repo whose references to `Widget` are
// concentrated in an alphabetically-early directory (a_first/) with a single
// straggler in a later directory (z_last/). Because refs are returned in
// rel_path order, the old investigate cap of 20 gave every slot to a_first/
// and z_last never surfaced — the report's own repro for refs in tests/
// directories being invisible.
func writeCERefFixture(t *testing.T, firstRefs int) string {
	t.Helper()
	dir := t.TempDir()

	for _, sub := range []string{"a_first", "z_last"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var lib strings.Builder
	lib.WriteString("typedef struct { int x; } Widget;\n")
	for i := 0; i < firstRefs; i++ {
		fmt.Fprintf(&lib, "static Widget w%d;\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "a_first", "lib.c"), []byte(lib.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "z_last", "app.c"), []byte("static Widget g;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func widgetSymbol(t *testing.T, dbPath string) SymbolResult {
	t.Helper()
	found, err := SearchSymbols(dbPath, SearchQuery{Text: "Widget", Exact: true, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range found {
		if s.Kind == "type" {
			return s
		}
	}
	t.Fatalf("Widget type symbol not found: %+v", found)
	return SymbolResult{}
}

// InvestigateResolved must surface references from every file, not just the
// alphabetically-earliest ones: with 26 refs (25 in a_first/, 1 in z_last/)
// the refs panel shows all of them and reports the honest total.
func TestInvestigateRefsSpanAllFiles(t *testing.T) {
	defer CloseAll()

	dir := writeCERefFixture(t, 25)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := Index(dir, dbPath, Options{Workers: 1, Force: true}); err != nil {
		t.Fatal(err)
	}

	inv, err := InvestigateResolved(dbPath, widgetSymbol(t, dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if inv.RefsTruncated {
		t.Fatalf("26 refs should fit under the overview cap, got truncated")
	}
	if inv.RefTotal != 26 {
		t.Errorf("RefTotal = %d, want 26", inv.RefTotal)
	}
	if len(inv.Refs) != 26 {
		t.Errorf("len(Refs) = %d, want 26", len(inv.Refs))
	}
	var sawLast bool
	for _, r := range inv.Refs {
		if strings.Contains(r.RelPath, "z_last/") {
			sawLast = true
			break
		}
	}
	if !sawLast {
		t.Fatalf("refs panel missed z_last/app.c; all refs matched a_first only")
	}
}

// When a symbol has more refs than the overview cap, RefsTruncated must be
// set and RefTotal must report the true count instead of silently showing a
// subset that reads as "all references".
func TestInvestigateRefsTruncationIsReported(t *testing.T) {
	defer CloseAll()

	dir := writeCERefFixture(t, 205)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := Index(dir, dbPath, Options{Workers: 1, Force: true}); err != nil {
		t.Fatal(err)
	}

	inv, err := InvestigateResolved(dbPath, widgetSymbol(t, dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if !inv.RefsTruncated {
		t.Fatalf("206 refs exceed the overview cap; RefsTruncated should be true")
	}
	if inv.RefTotal != 206 {
		t.Errorf("RefTotal = %d, want 206", inv.RefTotal)
	}
	if len(inv.Refs) != refsOverviewCap {
		t.Errorf("len(Refs) = %d, want capped at %d", len(inv.Refs), refsOverviewCap)
	}
}
