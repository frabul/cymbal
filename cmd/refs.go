package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/1broseidon/cymbal/index"
	"github.com/spf13/cobra"
)

var refsCmd = &cobra.Command{
	Use:   "refs <symbol> [symbol2 ...]",
	Short: "Find references to a symbol (best-effort)",
	Long: `Find files and lines that reference a symbol name.

Default: shows call-expression references across indexed files.
--importers: shows files that import the file defining this symbol.
--impact: shorthand for --importers --depth 2 (transitive impact).

Supports batch: cymbal refs Foo Bar Baz
Also accepts newline-separated names on stdin via --stdin:
  cymbal outline foo.go -s --names | cymbal refs --stdin

Note: references are best-effort based on AST name matching, not semantic analysis.`,
	Args: cobra.MinimumNArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		plan := resolveDBs(cmd)
		ensureFresh(plan.Primary)
		jsonOut := getJSONFlag(cmd)
		importers, _ := cmd.Flags().GetBool("importers")
		impact, _ := cmd.Flags().GetBool("impact")
		depth, _ := cmd.Flags().GetInt("depth")
		limit, _ := cmd.Flags().GetInt("limit")
		ctx, _ := cmd.Flags().GetInt("context")
		includes, _ := cmd.Flags().GetStringArray("path")
		excludes, _ := cmd.Flags().GetStringArray("exclude")
		fileScope, _ := cmd.Flags().GetString("file")
		if fileScope != "" {
			includes = append(includes, fileScope)
		}

		if impact {
			importers = true
			if depth < 2 {
				depth = 2
			}
		}

		names, err := collectSymbols(cmd, args)
		if err != nil {
			return err
		}

		for i, name := range names {
			if i > 0 {
				fmt.Println()
			}
			// Seed-only federation: route each name to whichever DB owns
			// it; refs/importers stay within that DB (non-goal #1).
			entry, _ := findSymbolEntry(plan, name)
			var err error
			if importers {
				err = refsImporters(entry.Path, name, depth, limit, jsonOut, includes, excludes, entry.Label())
			} else {
				err = refsSymbol(entry.Path, name, limit, ctx, jsonOut, includes, excludes, entry.Label())
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
			}
		}
		return nil
	},
}

func init() {
	refsCmd.Flags().Bool("importers", false, "find files that import the defining file")
	refsCmd.Flags().Bool("impact", false, "transitive impact analysis (--importers --depth 2)")
	refsCmd.Flags().IntP("depth", "D", 1, "import chain depth for --importers (max 3)")
	refsCmd.Flags().IntP("limit", "n", 20, "max results")
	refsCmd.Flags().IntP("context", "C", 1, "lines of context around each call site (0 for single-line)")
	refsCmd.Flags().StringArray("path", nil, "include only results whose path matches this glob (repeatable)")
	refsCmd.Flags().StringArray("exclude", nil, "exclude results whose path matches this glob (repeatable)")
	refsCmd.Flags().String("file", "", "restrict refs to files that import or include the given path fragment")
	addStdinFlag(refsCmd)
	rootCmd.AddCommand(refsCmd)
}

func refsSymbol(dbPath, name string, limit, ctx int, jsonOut bool, includes, excludes []string, worktreeLabel string) error {
	fetchLimit := widenPathFilterLimit(limit, len(includes) > 0 || len(excludes) > 0)
	results, err := index.FindReferences(dbPath, name, fetchLimit)
	if err != nil {
		return err
	}

	results = filterByPath(results, func(r index.RefResult) string { return r.RelPath }, includes, excludes)
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	// When the limit cut the result set short, say so: a truncation that
	// happens to land mid-file (results are ordered by path) can otherwise
	// look like the symbol has no references in later directories such as
	// tests/. Only when no path filters are in play — a filtered count would
	// need to mirror the same glob logic.
	if len(results) == 0 {
		fmt.Fprintf(os.Stderr, "No references found for '%s'.\n", name)
		return nil
	}

	// dbTotal is the true number of ref rows in the index; reported in meta
	// and as a note whenever the limit hid part of the result set.
	var dbTotal int
	if limit > 0 && len(includes) == 0 && len(excludes) == 0 && len(results) >= limit {
		if total, err := index.CountReferences(dbPath, name); err == nil && total > len(results) {
			dbTotal = total
			fmt.Fprintf(os.Stderr, "note: showing %d of %d references (pass -n N for more)\n", len(results), dbTotal)
		}
	}

	enriched := enrichRefs(results, ctx)

	var refs []refLine
	for _, r := range results {
		ctxLines, ctxStart := readSourceContext(r.File, r.Line, ctx)
		refs = append(refs, refLine{
			relPath:      r.RelPath,
			line:         r.Line,
			text:         strings.TrimSpace(readSourceLine(r.File, r.Line)),
			contextLines: ctxLines,
			contextStart: ctxStart,
		})
	}
	lines, groups := dedupRefLines(refs)

	var content strings.Builder
	for _, l := range lines {
		content.WriteString(l)
		content.WriteByte('\n')
	}

	meta := []kv{{"symbol", name}}
	if groups < len(results) {
		meta = append(meta, kv{"groups", fmt.Sprintf("%d", groups)})
		// When truncated, total_refs is the true index total, not the subset.
		metaTotal := len(results)
		if dbTotal > metaTotal {
			metaTotal = dbTotal
		}
		meta = append(meta, kv{"total_refs", fmt.Sprintf("%d", metaTotal)})
	} else {
		meta = append(meta, kv{"ref_count", fmt.Sprintf("%d", len(results))})
	}
	if worktreeLabel != "" {
		meta = append(meta, kv{"worktree", worktreeLabel})
	}
	return renderJSONOrFrontmatter(
		jsonOut,
		enriched,
		meta,
		content.String(),
	)
}

func refsImporters(dbPath, name string, depth, limit int, jsonOut bool, includes, excludes []string, worktreeLabel string) error {
	fetchLimit := widenPathFilterLimit(limit, len(includes) > 0 || len(excludes) > 0)
	results, err := index.FindImporters(dbPath, name, depth, fetchLimit)
	if err != nil {
		return err
	}

	results = filterByPath(results, func(r index.ImporterResult) string { return r.RelPath }, includes, excludes)
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	if len(results) == 0 {
		fmt.Fprintf(os.Stderr, "No importers found for '%s'.\n", name)
		return nil
	}

	meta := []kv{
		{"symbol", name},
		{"importer_count", fmt.Sprintf("%d", len(results))},
	}
	if worktreeLabel != "" {
		meta = append(meta, kv{"worktree", worktreeLabel})
	}
	return renderJSONOrFrontmatter(
		jsonOut,
		results,
		meta,
		formatImporterResults(results),
	)
}
