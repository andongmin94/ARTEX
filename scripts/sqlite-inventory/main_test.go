package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectGoExcludesComments(t *testing.T) {
	r, err := inspectGo("plain.go", []byte("package fixture\n// SELECT 1::bigint FOR UPDATE\n/* pg_advisory_lock */\nconst x = 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.features) != 0 || r.literals != 0 || r.calls != 0 {
		t.Fatalf("comments became candidates: %+v", r)
	}
}

func TestInspectGoTracksImportsStringsAndBoundaries(t *testing.T) {
	source := "package fixture\n" +
		"import pg \"github.com/jackc/pgx/v5/pgconn\"\n" +
		"func save() {\n" +
		"tx, _ := db.BeginTx(ctx, nil)\n" +
		"defer tx.Rollback()\n" +
		"tx.ExecContext(ctx, `SELECT id::bigint FROM t WHERE id=ANY($1) FOR UPDATE`)\n" +
		"tx.Commit()\n}\n"
	r, err := inspectGo("db/save.go", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	for feature, line := range map[string]int{"pg-driver": 2, "pg-cast": 6, "pg-array": 6, "pg-row-lock": 6} {
		if r.features[feature] != line {
			t.Errorf("%s line = %d, want %d", feature, r.features[feature], line)
		}
	}
	if r.literals != 1 || r.calls != 1 || len(r.boundaries) != 3 {
		t.Fatalf("counts: %+v", r)
	}
	if r.boundaries[0] != "save:db.BeginTx@4" {
		t.Fatalf("boundary location: %v", r.boundaries)
	}
}

func TestInspectGoRecognizesInterpretedFragmentsAndTestSkips(t *testing.T) {
	source := "package fixture\nfunc testDB(t *testing.T) {\n" +
		"dsn := os.Getenv(\"ARTEX_PG_DSN\")\n" +
		"if dsn == \"\" { t.Skip(\"needs DB\") }\n" +
		"db.Query(\"SELECT id \\n\" + \"FROM tasks WHERE name ILIKE $1\")\n" +
		"url.Query()\n}\n"
	r, err := inspectGo("server/store_test.go", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if r.features["pg-config"] != 3 || r.features["pg-query"] != 5 || len(r.skips) != 1 || !r.test || r.calls != 1 {
		t.Fatalf("report: %+v", r)
	}
}

func TestSurveySchemaAndDeterministicReport(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "db/schema.sql", "-- CREATE TABLE false_table(id int);\nCREATE TABLE IF NOT EXISTS assets (id BIGSERIAL);\nCREATE TRIGGER trg_assets BEFORE UPDATE ON assets;\n")
	writeFixture(t, root, "server/app.go", "package fixture\nimport _ \"github.com/Autumn-27/artex/db\"\n")
	writeFixture(t, root, "plain.go", "package fixture\n")
	for _, dir := range []string{".git", "node_modules", "vendor", ".next", "out", "dist", "scripts/sqlite-inventory"} {
		writeFixture(t, root, dir+"/ignored.go", "invalid Go that must not be parsed")
	}
	inv, err := survey(root)
	if err != nil {
		t.Fatal(err)
	}
	if inv.sources != 3 || len(inv.files) != 2 {
		t.Fatalf("counts: %+v", inv)
	}
	if got := strings.Join(inv.files[0].tables, ","); got != "assets" {
		t.Fatalf("tables = %q", got)
	}
	if got := strings.Join(inv.files[0].triggers, ","); got != "trg_assets" {
		t.Fatalf("triggers = %q", got)
	}
	var first, second bytes.Buffer
	if err := writeReport(&first, inv); err != nil {
		t.Fatal(err)
	}
	again, err := survey(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeReport(&second, again); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() || strings.Contains(first.String(), root) {
		t.Fatal("report is unstable or includes absolute root")
	}
}

func TestSurveyFailsForInvalidGo(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "broken.go", "package fixture\nfunc broken(")
	if _, err := survey(root); err == nil || !strings.Contains(err.Error(), "broken.go") {
		t.Fatalf("expected parse error naming source: %v", err)
	}
}

func TestSurveyRejectsEmptyOrMissingRoots(t *testing.T) {
	root := t.TempDir()
	if _, err := survey(root); err == nil {
		t.Fatal("empty scan reported success")
	}
	if _, err := survey(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing root reported success")
	}
}

func TestReportDoesNotPrintSourceValues(t *testing.T) {
	r, err := inspectGo("config.go", []byte("package fixture\nconst dsn = \"postgres://user:SECRET@localhost/private\"\nfunc save() { openDB(\"SECRET\").BeginTx(ctx,nil) }\n"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := writeReport(&out, inventory{sources: 1, files: []fileReport{r}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "SECRET") || strings.Contains(out.String(), "postgres://") {
		t.Fatal("source value leaked into report")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output failed") }

func TestReportPropagatesWriteFailure(t *testing.T) {
	if err := writeReport(failingWriter{}, inventory{}); err == nil {
		t.Fatal("output failure reported success")
	}
}

func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
