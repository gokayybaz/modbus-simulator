// Package archtest enforces the package dependency rules of ADR-0005.
package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "modbus-simulator/"

// forbidden lists, per internal package, the internal packages it must not
// import (including from tests). Packages that do not exist yet are skipped,
// so rules for later phases can be declared up front.
var forbidden = map[string][]string{
	"internal/modbus":     {"internal/plc", "internal/simulation", "internal/mapping", "internal/config", "internal/plant", "internal/scenarios"},
	"internal/config":     {"internal/modbus", "internal/plc", "internal/simulation", "internal/mapping", "internal/plant", "internal/scenarios"},
	"internal/simulation": {"internal/modbus", "internal/plc", "internal/mapping", "internal/plant"},
	"internal/plc":        {"internal/simulation", "internal/mapping", "internal/config", "internal/plant"},
	"internal/mapping":    {"internal/config", "internal/plant"},
	"internal/scenarios":  {"internal/modbus", "internal/plc", "internal/mapping", "internal/plant", "internal/config"},
}

func TestLayering(t *testing.T) {
	root := filepath.Join("..", "..")
	checked := 0
	for pkg, deny := range forbidden {
		dir := filepath.Join(root, pkg)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		checked++
		for path, file := range imports(t, dir) {
			for _, d := range deny {
				if path == module+d || strings.HasPrefix(path, module+d+"/") {
					t.Errorf("%s imports %s (forbidden by ADR-0005)", file, path)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no packages checked; wrong root?")
	}
}

// imports returns import path -> file for all .go files under dir (recursive).
func imports(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			out[path] = p
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
