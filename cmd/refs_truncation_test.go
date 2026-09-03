package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1broseidon/cymbal/index"
)

// refsFixture builds a C repo whose 26 references to `Widget` cluster in an
// alphabetically-early directory (a_first/) with one straggler in z_last/ —
// the same shape as master/* vs tests/* in the field report. Returns the
// repo dir and its DB path.
func refsFixture(t *testing.T) (dir, dbPath string) {
	t.Helper()
	defer index.CloseAll()

	dir = t.TempDir()
	t.Setenv("CYMBAL_CACHE_DIR", t.TempDir())

	for _, sub := range []string{"a_first", "z_last"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var lib strings.Builder
	lib.WriteString("typedef struct { int x; } Widget;\n")
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&lib, "static Widget w%d;\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "a_first", "lib.c"), []byte(lib.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "z_last", "app.c"), []byte("static Widget g;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath, err := index.RepoDBPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Index(dir, dbPath, index.Options{Workers: 1, Force: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(index.CloseAll)
	return dir, dbPath
}

// refs with the default limit truncates at 20 refs, all from a_first/ (they
// sort first); the hint on stderr must say that more exist so nobody
// concludes the symbol has no references in later directories.
func TestRefsReportsTruncationHint(t *testing.T) {
	_, dbPath := refsFixture(t)

	stdout, stderr, err := captureProcessOutput(t, func() error {
		return refsSymbol(dbPath, "Widget", 20, 1, false, nil, nil, "")
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "showing 20 of 26 references") {
		t.Errorf("stderr missing truncation hint, got: %q", stderr)
	}
	if strings.Contains(stdout, "z_last/") {
		t.Errorf("default 20-ref limit should not reach z_last/, got:\n%s", stdout)
	}
}

// Raising -n surfaces the refs in z_last/ alongside the 20 from a_first/.
func TestRefsLimitSurfacesLaterFiles(t *testing.T) {
	_, dbPath := refsFixture(t)

	stdout, stderr, err := captureProcessOutput(t, func() error {
		return refsSymbol(dbPath, "Widget", 200, 1, false, nil, nil, "")
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "showing") {
		t.Errorf("with -n 200 all 26 refs fit; unexpected hint: %q", stderr)
	}
	if !strings.Contains(stdout, "z_last/app.c") {
		t.Errorf("raised limit should include z_last/app.c, got:\n%s", stdout)
	}
}

// investigate's refs panel must include the later directory's references and
// show the honest total instead of a 20-ref subset from a_first/ only.
func TestInvestigateShowsRefsAcrossDirectories(t *testing.T) {
	_, dbPath := refsFixture(t)

	stdout, _, err := captureProcessOutput(t, func() error {
		return investigateOnePrint(dbPath, "Widget", false, "", index.ResolveScopeFamily)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "# References (26)") {
		t.Errorf("expected full refs count, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "z_last/app.c") {
		t.Errorf("investigate missed z_last/app.c, got:\n%s", stdout)
	}
}
