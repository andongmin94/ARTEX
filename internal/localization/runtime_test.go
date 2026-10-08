package localization

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// Foreign provider messages and ICP syntax are external input, not translated
// UI. Keep the exceptions scoped to their exact source file and literal.
func TestBackendRuntimeTextIsLocalized(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]map[string]bool{
		"agent/provider.go":   {"余额不足": true, "额度不足": true, "额度已用尽": true, "欠费": true},
		"db/company_scope.go": {"备案": true},
	}
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if name != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "data" || entry.Name() == "dist" || entry.Name() == "out") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		positions := token.NewFileSet()
		source, err := parser.ParseFile(positions, name, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(source, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Error(err)
				return true
			}
			if allowed[rel][value] {
				return true
			}
			for _, r := range value {
				if unicode.Is(unicode.Han, r) {
					t.Errorf("untranslated runtime literal at %s:%d: %q", rel, positions.Position(literal.Pos()).Line, value)
					break
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
