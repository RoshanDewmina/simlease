package errortriple

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Finding is one NewError call site whose code, message, or fix argument is an
// empty string literal.
type Finding struct {
	File    string
	Line    int
	Snippet string
}

// Audit walks every non-test .go file under rootDir (excluding
// test/fixtures/SampleApp) and returns a Finding for each NewError call whose
// first, second, or third argument is the empty string literal "".
//
// Calls that pass a non-literal expression are not reported; see the package
// doc for the scope boundary with runtime validation.
func Audit(rootDir string) ([]Finding, error) {
	findings, _, err := audit(rootDir)
	return findings, err
}

// audit is the shared walk used by Audit and tests; examined is the number of
// NewError call expressions inspected (valid and invalid alike).
func audit(rootDir string) (findings []Finding, examined int, err error) {
	fset := token.NewFileSet()
	err = filepath.WalkDir(rootDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if name == "SampleApp" && isUnderFixtures(rootDir, path) {
				return filepath.SkipDir
			}
			if name == "vendor" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isNewErrorCall(call) {
				return true
			}
			examined++
			if finding := checkNewErrorCall(fset, path, rootDir, call); finding != nil {
				findings = append(findings, *finding)
			}
			return true
		})
		return nil
	})
	return findings, examined, err
}

func isUnderFixtures(rootDir, path string) bool {
	rel, err := filepath.Rel(rootDir, path)
	if err != nil {
		return false
	}
	return strings.Contains(filepath.ToSlash(rel), "test/fixtures/")
}

func isNewErrorCall(call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name == "NewError"
	case *ast.SelectorExpr:
		return fun.Sel != nil && fun.Sel.Name == "NewError"
	default:
		return false
	}
}

func checkNewErrorCall(fset *token.FileSet, path, rootDir string, call *ast.CallExpr) *Finding {
	if len(call.Args) < 3 {
		return nil
	}
	for _, arg := range call.Args[:3] {
		if isEmptyStringLit(arg) {
			pos := fset.Position(call.Pos())
			rel := path
			if r, err := filepath.Rel(rootDir, path); err == nil {
				rel = r
			}
			return &Finding{
				File:    rel,
				Line:    pos.Line,
				Snippet: callSnippet(fset, call),
			}
		}
	}
	return nil
}

func isEmptyStringLit(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return lit.Value == `""`
	}
	return s == ""
}

func callSnippet(fset *token.FileSet, call *ast.CallExpr) string {
	start := fset.Position(call.Pos())
	end := fset.Position(call.End())
	data, err := os.ReadFile(start.Filename)
	if err != nil {
		return "NewError(...)"
	}
	lines := strings.Split(string(data), "\n")
	if start.Line < 1 || start.Line > len(lines) {
		return "NewError(...)"
	}
	if start.Line == end.Line {
		line := lines[start.Line-1]
		if start.Column >= 1 && end.Column >= start.Column && end.Column-1 <= len(line) {
			return strings.TrimSpace(line[start.Column-1 : end.Column-1])
		}
		return strings.TrimSpace(line)
	}
	var b strings.Builder
	for i := start.Line - 1; i < end.Line && i < len(lines); i++ {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strings.TrimSpace(lines[i]))
	}
	return b.String()
}
