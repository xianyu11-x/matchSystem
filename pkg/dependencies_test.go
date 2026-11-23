package pkg_test

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// modulePath 从仓库根目录的 go.mod 读取当前模块路径。
func modulePath(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "module" {
			if strings.HasPrefix(fields[1], "\"") || strings.HasPrefix(fields[1], "`") {
				return strconv.Unquote(fields[1])
			}
			return fields[1], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s 中缺少 module 声明", path)
}

// inspectImports 直接遍历源码，因此不会受当前平台或构建标签影响。
func inspectImports(root, module string, report func(token.Position, string)) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			insideModule := importPath == module || strings.HasPrefix(importPath, module+"/")
			insidePkg := importPath == module+"/pkg" || strings.HasPrefix(importPath, module+"/pkg/")
			if insideModule && !insidePkg {
				report(fileSet.Position(spec.Pos()), importPath)
			}
		}
		return nil
	})
}

func TestPublicPackageImportBoundary(t *testing.T) {
	module, err := modulePath(filepath.Join("..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := inspectImports(".", module, func(position token.Position, importPath string) {
		t.Errorf("%s 导入了同仓 pkg 以外的包 %q", position, importPath)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestImportBoundaryChecksInactiveTestFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inactive_test.go")
	content := `//go:build unsupported_platform

package example

import (
	"fmt"
	"example.com/repository/pkg/client"
	"example.com/repository-plugin"
	"example.com/repository/internal/simulator"
	"example.com/repository/pkg_extra"
)
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var forbidden []string
	if err := inspectImports(dir, "example.com/repository", func(position token.Position, importPath string) {
		if position.Filename != path || position.Line == 0 {
			t.Errorf("违规导入的位置不完整：%v", position)
		}
		forbidden = append(forbidden, importPath)
	}); err != nil {
		t.Fatal(err)
	}
	if len(forbidden) != 2 || forbidden[0] != "example.com/repository/internal/simulator" || forbidden[1] != "example.com/repository/pkg_extra" {
		t.Fatalf("非当前平台测试文件的违规导入 = %v", forbidden)
	}
}
