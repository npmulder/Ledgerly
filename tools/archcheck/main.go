package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	moduleDependencies = map[string]map[string]struct{}{
		"advisor":      dependencySet("jurisdiction", "invoicing", "banking", "dla", "dividends", "reports", "moneyfx", "identity"),
		"app":          dependencySet("demo", "identity", "invoicing", "jurisdiction", "ledger"),
		"banking":      dependencySet("ledger", "moneyfx", "invoicing", "dla"),
		"demo":         dependencySet(),
		"dividends":    dependencySet("ledger", "reports", "jurisdiction", "identity"),
		"dla":          dependencySet("ledger", "jurisdiction"),
		"identity":     dependencySet(),
		"invoicing":    dependencySet("moneyfx", "ledger", "jurisdiction", "identity"),
		"it":           dependencySet("app", "demo", "identity", "invoicing", "ledger", "dla"),
		"jurisdiction": dependencySet(),
		"ledger":       dependencySet(),
		"moneyfx":      dependencySet("ledger"),
		"platform":     dependencySet(),
		"reports":      dependencySet("ledger", "jurisdiction", "identity", "invoicing"),
	}
	rateLiteralAllowedModules = []string{"jurisdiction"}
	guardedLiterals           = []string{"0.20", "0.10", "0.21", "6500", "14750"}
	execCommand               = exec.Command
)

type goPackage struct {
	ImportPath   string
	Imports      []string
	TestImports  []string
	XTestImports []string
	Error        *goListError
	DepsErrors   []goListError
}

type goListError struct {
	ImportStack []string
	Pos         string
	Err         string
}

type finding struct {
	Path    string
	Line    int
	Column  int
	Message string
}

func (f finding) String() string {
	location := f.Path
	if f.Line > 0 {
		location = fmt.Sprintf("%s:%d:%d", location, f.Line, f.Column)
	}
	return fmt.Sprintf("%s: %s", location, f.Message)
}

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "arch":
		err = runArch(os.Args[2:])
	case "rates":
		err = runRates(os.Args[2:])
	default:
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: archcheck <arch|rates>")
}

func runArch(args []string) error {
	flags := flag.NewFlagSet("arch", flag.ExitOnError)
	module := flags.String("module", "", "module import path; defaults to `go list -m`")
	if err := flags.Parse(args); err != nil {
		return err
	}

	modulePath := *module
	if modulePath == "" {
		var err error
		modulePath, err = currentModulePath()
		if err != nil {
			return err
		}
	}

	patterns := flags.Args()
	if len(patterns) == 0 {
		patterns = []string{"./internal/..."}
	}

	pkgs, err := listPackages(patterns)
	if err != nil {
		return err
	}
	findings := checkPackageBoundaries(pkgs, modulePath)
	if len(findings) > 0 {
		printFindings(os.Stderr, findings)
		return fmt.Errorf("architecture boundary check failed with %d violation(s)", len(findings))
	}
	if goListFindings := packageLoadFindings(pkgs); len(goListFindings) > 0 {
		printFindings(os.Stderr, goListFindings)
		return fmt.Errorf("go package loading failed with %d error(s)", len(goListFindings))
	}
	return nil
}

func runRates(args []string) error {
	flags := flag.NewFlagSet("rates", flag.ExitOnError)
	root := flags.String("root", "internal", "internal module root to scan")
	if err := flags.Parse(args); err != nil {
		return err
	}

	findings, err := checkRateLiterals(*root, rateLiteralAllowedModules, guardedLiterals)
	if err != nil {
		return err
	}
	if len(findings) > 0 {
		printFindings(os.Stderr, findings)
		return fmt.Errorf("literal-rate guard failed with %d violation(s)", len(findings))
	}
	return nil
}

func currentModulePath() (string, error) {
	cmd := execCommand("go", "list", "-m")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go list -m failed: %w\n%s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func listPackages(patterns []string) ([]goPackage, error) {
	args := append([]string{"list", "-e", "-json"}, patterns...)
	cmd := execCommand("go", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil && stdout.Len() == 0 {
		return nil, fmt.Errorf("go list failed: %w\n%s", err, stderr.String())
	}

	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var pkgs []goPackage
	for {
		var pkg goPackage
		if err := dec.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if stderr.Len() > 0 {
				return nil, fmt.Errorf("decode go list JSON: %w\n%s", err, stderr.String())
			}
			return nil, fmt.Errorf("decode go list JSON: %w", err)
		}
		pkgs = append(pkgs, pkg)
	}
	if err != nil && len(pkgs) == 0 {
		return nil, fmt.Errorf("go list failed: %w\n%s", err, stderr.String())
	}
	return pkgs, nil
}

func checkPackageBoundaries(pkgs []goPackage, modulePath string) []finding {
	var findings []finding
	for _, pkg := range pkgs {
		sourceModule, ok := internalModule(pkg.ImportPath, modulePath)
		if !ok {
			continue
		}

		imports := uniqueImports(pkg.Imports, pkg.TestImports, pkg.XTestImports)
		for _, imported := range imports {
			segments, ok := internalSegments(imported, modulePath)
			if !ok {
				continue
			}
			if allowedInternalImport(sourceModule, segments) {
				continue
			}

			findings = append(findings, finding{
				Path: pkg.ImportPath,
				Message: fmt.Sprintf(
					"imports %s; cross-module imports must use declared internal/<module> root dependencies, internal/platform/..., or internal/moneyfx/money",
					imported,
				),
			})
		}
	}

	sortFindings(findings)
	return findings
}

func internalModule(importPath, modulePath string) (string, bool) {
	segments, ok := internalSegments(importPath, modulePath)
	if !ok {
		return "", false
	}
	return segments[0], true
}

func internalSegments(importPath, modulePath string) ([]string, bool) {
	prefix := modulePath + "/internal/"
	if !strings.HasPrefix(importPath, prefix) {
		return nil, false
	}
	rest := strings.TrimPrefix(importPath, prefix)
	if rest == "" {
		return nil, false
	}
	return strings.Split(rest, "/"), true
}

func allowedInternalImport(sourceModule string, importedSegments []string) bool {
	importedModule := importedSegments[0]
	if importedModule == sourceModule {
		return true
	}
	if importedModule == "platform" {
		return true
	}
	if importedModule == "moneyfx" && len(importedSegments) == 2 && importedSegments[1] == "money" {
		return true
	}
	if len(importedSegments) != 1 {
		return false
	}
	dependencies, ok := moduleDependencies[sourceModule]
	if !ok {
		return false
	}
	_, ok = dependencies[importedModule]
	return ok
}

func dependencySet(modules ...string) map[string]struct{} {
	dependencies := make(map[string]struct{}, len(modules))
	for _, module := range modules {
		dependencies[module] = struct{}{}
	}
	return dependencies
}

func uniqueImports(groups ...[]string) []string {
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, importPath := range group {
			seen[importPath] = struct{}{}
		}
	}

	imports := make([]string, 0, len(seen))
	for importPath := range seen {
		imports = append(imports, importPath)
	}
	sort.Strings(imports)
	return imports
}

func packageLoadFindings(pkgs []goPackage) []finding {
	var findings []finding
	seen := make(map[string]struct{})
	for _, pkg := range pkgs {
		if pkg.Error != nil {
			findings = appendGoListFinding(findings, seen, pkg.ImportPath, *pkg.Error)
		}
		for _, depErr := range pkg.DepsErrors {
			findings = appendGoListFinding(findings, seen, pkg.ImportPath, depErr)
		}
	}
	sortFindings(findings)
	return findings
}

func appendGoListFinding(findings []finding, seen map[string]struct{}, importPath string, problem goListError) []finding {
	message := problem.Err
	if len(problem.ImportStack) > 0 {
		message = fmt.Sprintf("%s (import stack: %s)", message, strings.Join(problem.ImportStack, " -> "))
	}

	path := importPath
	if problem.Pos != "" {
		path = problem.Pos
	}

	key := path + "\x00" + message
	if _, ok := seen[key]; ok {
		return findings
	}
	seen[key] = struct{}{}
	return append(findings, finding{
		Path:    path,
		Message: message,
	})
}

func checkRateLiterals(root string, allowedModules []string, literals []string) ([]finding, error) {
	pattern := literalPattern(literals)
	allowed := dependencySet(allowedModules...)
	var findings []finding

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			module, err := moduleForPath(root, path)
			if err != nil {
				return err
			}
			if _, ok := allowed[module]; ok {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		module, err := moduleForPath(root, path)
		if err != nil {
			return err
		}
		if _, ok := allowed[module]; ok {
			return nil
		}

		fileFindings, err := scanRateFile(path, pattern)
		if err != nil {
			return err
		}
		findings = append(findings, fileFindings...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sortFindings(findings)
	return findings, nil
}

func moduleForPath(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	segments := strings.Split(filepath.ToSlash(rel), "/")
	if len(segments) == 0 || segments[0] == "." || segments[0] == "" {
		return "", nil
	}
	return segments[0], nil
}

func literalPattern(literals []string) *regexp.Regexp {
	escaped := make([]string, 0, len(literals))
	for _, literal := range literals {
		escaped = append(escaped, regexp.QuoteMeta(literal))
	}
	return regexp.MustCompile(`(^|[^0-9A-Za-z_.])(` + strings.Join(escaped, "|") + `)([^0-9A-Za-z_.]|$)`)
}

func scanRateFile(path string, pattern *regexp.Regexp) ([]finding, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	var findings []finding
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := scanner.Text()
		for _, match := range pattern.FindAllStringSubmatchIndex(line, -1) {
			literalStart := match[4]
			literalEnd := match[5]
			findings = append(findings, finding{
				Path:    path,
				Line:    lineNumber,
				Column:  literalStart + 1,
				Message: fmt.Sprintf("guarded compliance literal %q belongs in the jurisdiction pack", line[literalStart:literalEnd]),
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return findings, nil
}

func importsFromFile(path string) ([]string, error) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}

	imports := make([]string, 0, len(file.Imports))
	for _, importSpec := range file.Imports {
		importPath, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			return nil, err
		}
		imports = append(imports, importPath)
	}
	return imports, nil
}

func printFindings(w io.Writer, findings []finding) {
	for _, finding := range findings {
		_, _ = fmt.Fprintln(w, finding)
	}
}

func sortFindings(findings []finding) {
	sort.Slice(findings, func(i, j int) bool {
		return findings[i].String() < findings[j].String()
	})
}
