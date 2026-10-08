// Command sqlite-inventory reports storage-porting candidates without opening a
// database or executing application code. It is a review aid, not a SQL parser
// or proof of PostgreSQL independence. Remove it when the SQLite port is complete.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type rule struct {
	name string
	re   *regexp.Regexp
}

var rules = []rule{
	{"pg-cast", regexp.MustCompile(`::[a-zA-Z]`)},
	{"pg-types", regexp.MustCompile(`(?i)\b(BIGSERIAL|SERIAL|TIMESTAMPTZ|JSONB|CIDR|INET)\b`)},
	{"pg-array", regexp.MustCompile(`(?i)\b(ANY|ALL|UNNEST|ARRAY_APPEND|ARRAY_REMOVE|ARRAY_AGG|ARRAY_LENGTH|ARRAY_TO_STRING|CARDINALITY)\s*\(|\b(BIGINT|INTEGER|TEXT|JSONB)\s*\[\]|\bARRAY\s*\[`)},
	{"pg-json", regexp.MustCompile(`(?i)\b(JSONB_\w+|JSON_POPULATE_RECORDSET|JSON_TO_RECORDSET|ROW_TO_JSON|TO_JSONB)\s*\(|@>|<@`)},
	{"pg-network", regexp.MustCompile(`(?i)\b(TRY_INET|SET_MASKLEN|MASKLEN)\s*\(|::(?:inet|cidr)\b|>>=|<<=`)},
	{"pg-row-lock", regexp.MustCompile(`(?i)\bFOR\s+(UPDATE|SHARE|NO\s+KEY\s+UPDATE|KEY\s+SHARE)\b|\bSKIP\s+LOCKED\b`)},
	{"pg-advisory", regexp.MustCompile(`(?i)\bpg_advisory_\w+`)},
	{"pg-catalog-ddl", regexp.MustCompile(`(?i)\b(pg_catalog|pg_constraint|pg_database|pg_class|pg_attribute|pg_indexes|information_schema|plpgsql|regclass|pg_get_\w+|pg_total_relation_size|setval)\b|\bDO\s+\$\$`)},
	{"pg-index", regexp.MustCompile(`(?i)\bUSING\s+(GIN|GIST)\b|\bINCLUDE\s*\(`)},
	{"pg-time", regexp.MustCompile(`(?i)\b(EXTRACT|DATE_TRUNC|TO_TIMESTAMP|MAKE_INTERVAL|CLOCK_TIMESTAMP)\s*\(|\bINTERVAL\s*'`)},
	{"pg-query", regexp.MustCompile(`(?i)\bILIKE\b|\bDISTINCT\s+ON\s*\(|\b(BOOL_OR|BOOL_AND|STRING_AGG|REGEXP_REPLACE|GREATEST|LEAST)\s*\(|\bDELETE\s+FROM\s+\w+(?:\s+\w+)?\s+USING\b`)},
	{"pg-config", regexp.MustCompile(`\bARTEX_(?:SMOKE_)?PG_DSN\b|postgres(?:ql)?://`)},
	{"sqlite", regexp.MustCompile(`(?i)\bPRAGMA\b|\bsqlite_(master|schema)\b|\bfts5\s*\(`)},
}

var sqlWords = regexp.MustCompile(`(?i)\b(SELECT\s+|INSERT\s+INTO\s+|UPDATE\s+\w+\s+SET\s+|DELETE\s+FROM\s+|CREATE\s+(?:TABLE|INDEX|TRIGGER|FUNCTION)\s+|ALTER\s+TABLE\s+|PRAGMA\s+)`)
var tableDecl = regexp.MustCompile(`(?im)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z_0-9]*)\s*\(`)
var triggerDecl = regexp.MustCompile(`(?im)^\s*CREATE\s+TRIGGER\s+([a-z_][a-z_0-9]*)\b`)

type fileReport struct {
	path       string
	test       bool
	literals   int
	calls      int
	features   map[string]int // first source line for each feature
	boundaries []string
	skips      []string
	tables     []string
	triggers   []string
}

type inventory struct {
	sources int
	files   []fileReport
}

func (r *fileReport) mark(name string, line int) {
	if old, ok := r.features[name]; !ok || line < old {
		r.features[name] = line
	}
}

func (r *fileReport) inspectText(text string, line int) {
	for _, rule := range rules {
		if rule.re.MatchString(text) {
			r.mark(rule.name, line)
		}
	}
	for _, match := range tableDecl.FindAllStringSubmatch(text, -1) {
		r.tables = append(r.tables, match[1])
	}
	for _, match := range triggerDecl.FindAllStringSubmatch(text, -1) {
		r.triggers = append(r.triggers, match[1])
	}
}

func inspectGo(path string, content []byte) (fileReport, error) {
	r := fileReport{path: path, test: strings.HasSuffix(path, "_test.go"), features: map[string]int{}}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, content, 0)
	if err != nil {
		return r, err
	}
	for _, imp := range f.Imports {
		name, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return r, err
		}
		line := fset.Position(imp.Pos()).Line
		switch {
		case name == "database/sql":
			r.mark("sql-import", line)
		case strings.HasPrefix(name, "github.com/jackc/pgx/"):
			r.mark("pg-driver", line)
		case name == "modernc.org/sqlite" || strings.HasPrefix(name, "modernc.org/sqlite/"):
			r.mark("sqlite-driver", line)
		case name == "github.com/Autumn-27/artex/db":
			r.mark("domain-store", line)
		}
	}
	// Inspect declarations separately so boundary locations include their owning
	// function. We deliberately do not infer types or resolve SQL string builders.
	for _, decl := range f.Decls {
		owner := "package"
		if fn, ok := decl.(*ast.FuncDecl); ok {
			owner = fn.Name.Name
		}
		ast.Inspect(decl, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.BasicLit:
				if n.Kind != token.STRING {
					break
				}
				text, err := strconv.Unquote(n.Value)
				if err != nil {
					break // ParseFile already rejects malformed string literals.
				}
				if sqlWords.MatchString(text) {
					r.literals++
				}
				r.inspectText(text, fset.Position(n.Pos()).Line)
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if !ok {
					break
				}
				name := sel.Sel.Name
				line := fset.Position(n.Pos()).Line
				location := fmt.Sprintf("%s:%s.%s@%d", owner, receiverName(sel.X), name, line)
				switch name {
				case "Exec", "ExecContext", "Query", "QueryContext", "QueryRow", "QueryRowContext", "Prepare", "PrepareContext":
					if len(n.Args) > 0 {
						r.calls++
					}
				case "Begin", "BeginTx", "Commit", "Rollback", "Conn":
					r.boundaries = append(r.boundaries, location)
				case "Skip", "Skipf", "SkipNow":
					if r.test {
						r.skips = append(r.skips, location)
					}
				}
			}
			return true
		})
	}
	return r, nil
}

// Never print call arguments or index expressions: a receiver can itself contain
// a DSN, API key, or other source value. Identifiers alone are sufficient here.
func receiverName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.SelectorExpr:
		return receiverName(n.X) + "." + n.Sel.Name
	default:
		return "<expr>"
	}
}

func survey(root string) (inventory, error) {
	var result inventory
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor", ".next", "out", "dist":
				return filepath.SkipDir
			}
			if rel == "scripts/sqlite-inventory" {
				return filepath.SkipDir // Do not inventory the detector's own fixtures.
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || (filepath.Ext(path) != ".go" && filepath.Ext(path) != ".sql") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result.sources++
		var r fileReport
		if filepath.Ext(path) == ".go" {
			r, err = inspectGo(rel, data)
		} else {
			r = fileReport{path: rel, features: map[string]int{}}
			r.inspectText(string(data), 1)
		}
		if err != nil {
			return err
		}
		if len(r.features)+r.literals+r.calls+len(r.boundaries) > 0 {
			result.files = append(result.files, r)
		}
		return nil
	})
	if err == nil && result.sources == 0 {
		err = fmt.Errorf("no Go or SQL sources found in %s", root)
	}
	return result, err
}

func writeReport(w io.Writer, inv inventory) error {
	var out strings.Builder
	fmt.Fprintln(&out, "# SQLite porting inventory")
	fmt.Fprintln(&out, "\nGenerated by `go run ./scripts/sqlite-inventory`. Review candidates, not proof of SQL compatibility.")
	fmt.Fprintln(&out, "Go comments are excluded by AST parsing; SQL files are inspected as text. Method names are not type-resolved, and dynamic SQL/imported stores require manual review. Locations point to a string literal or call, not necessarily a complete query. No source values or credentials are printed.")
	fmt.Fprintf(&out, "\nSources scanned: %d; candidate files: %d.\n", inv.sources, len(inv.files))
	fmt.Fprintln(&out, "\n| File | Test | SQL strings | Call candidates | Features (first line) |")
	fmt.Fprintln(&out, "| --- | --- | ---: | ---: | --- |")
	for _, r := range inv.files {
		var features []string
		for name, line := range r.features {
			features = append(features, fmt.Sprintf("%s@%d", name, line))
		}
		sort.Strings(features)
		fmt.Fprintf(&out, "| `%s` | %t | %d | %d | %s |\n", r.path, r.test, r.literals, r.calls, strings.Join(features, ", "))
	}
	fmt.Fprintln(&out, "\n## Transaction/connection call sites")
	for _, r := range inv.files {
		if len(r.boundaries) > 0 {
			fmt.Fprintf(&out, "\n- `%s`: %s\n", r.path, strings.Join(r.boundaries, "; "))
		}
	}
	fmt.Fprintln(&out, "\n## Test skips in candidate files (inspect setup helpers before porting)")
	for _, r := range inv.files {
		if len(r.skips) > 0 {
			fmt.Fprintf(&out, "\n- `%s`: %s\n", r.path, strings.Join(r.skips, "; "))
		}
	}
	fmt.Fprintln(&out, "\n## Schema declarations (includes test/inline schemas)")
	for _, r := range inv.files {
		if len(r.tables)+len(r.triggers) > 0 {
			fmt.Fprintf(&out, "\n- `%s`: tables=%s; triggers=%s\n", r.path, strings.Join(r.tables, ","), strings.Join(r.triggers, ","))
		}
	}
	_, err := io.WriteString(w, out.String())
	return err
}

func main() {
	root := flag.String("root", ".", "repository root to inspect (read-only)")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "use -root to select the repository")
		os.Exit(2)
	}
	inv, err := survey(*root)
	if err == nil {
		err = writeReport(os.Stdout, inv)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
