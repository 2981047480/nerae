// go_imports parses all production Go files, including files behind build tags.
package main

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type sourceImports struct {
	Directory string   `json:"directory"`
	File      string   `json:"file"`
	Imports   []string `json:"imports"`
}

func run(root string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	entries := []sourceImports{}
	err = filepath.WalkDir(root, func(file string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source symlink is not checked: %s", file)
		}
		if entry.IsDir() {
			if file != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" || entry.Name() == "node_modules" || entry.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		item := sourceImports{Directory: filepath.ToSlash(filepath.Dir(rel)), File: filepath.ToSlash(rel), Imports: []string{}}
		for _, imported := range parsed.Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return fmt.Errorf("parse import in %s: %w", rel, err)
			}
			item.Imports = append(item.Imports, value)
		}
		entries = append(entries, item)
		return nil
	})
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("no Go production source files found")
	}
	return json.NewEncoder(os.Stdout).Encode(entries)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go_imports API_ROOT")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
