package localization

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// SQL triggers produce UI messages without going through Go literals.
func TestSQLRuntimeTextIsLocalized(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "db", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for n, line := range strings.Split(string(content), "\n") {
		code, _, _ := strings.Cut(line, "--")
		// Existing historical input marker, not a new display label.
		code = strings.ReplaceAll(code, "'[模型]%'", "'historical-model-prefix'")
		for _, r := range code {
			if unicode.Is(unicode.Han, r) {
				t.Errorf("untranslated SQL runtime text at schema.sql:%d: %s", n+1, code)
				break
			}
		}
	}
}
