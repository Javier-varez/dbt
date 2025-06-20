package package_driver

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ModuleFile represents a parsed MODULE file
type ModuleFile struct {
	Name         string                `json:"name"`
	Dependencies map[string]Dependency `json:"dependencies"`
}

// Dependency represents a dependency declaration in a MODULE file
type Dependency struct {
	URL    string `json:"url"`
	Branch string `json:"branch,omitempty"`
	Tag    string `json:"tag,omitempty"`
	Hash   string `json:"hash,omitempty"`
}

// Workspace represents a dbt workspace with its dependencies
type Workspace struct {
	Root     string
	Module   *ModuleFile
	DepsDir  string
	BuildDir string
	Packages map[string]*Package // import path -> Package
}

// Package represents a Go package within the dbt workspace
type Package struct {
	ID          string   // unique identifier
	Name        string   // package name
	ImportPath  string   // import path
	Dir         string   // directory containing package
	GoFiles     []string // .go files
	Imports     []string // import declarations
	TestGoFiles []string // _test.go files
	TestImports []string // imports from test files
	Errors      []error  // parse errors
}

// FindWorkspace searches for a dbt workspace starting from the given directory
func FindWorkspace(dir string) (*Workspace, error) {
	for {
		moduleFile := filepath.Join(dir, "MODULE")
		if _, err := os.Stat(moduleFile); err == nil {
			return loadWorkspace(dir)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, fmt.Errorf("no MODULE file found")
}

// loadWorkspace loads a dbt workspace from the given root directory
func loadWorkspace(root string) (*Workspace, error) {
	moduleFile := filepath.Join(root, "MODULE")
	module, err := parseModuleFile(moduleFile)
	if err != nil {
		return nil, fmt.Errorf("parsing MODULE file: %w", err)
	}

	depsDir := filepath.Join(root, "DEPS")
	buildDir := filepath.Join(root, "BUILD")
	workspace := &Workspace{
		Root:     root,
		Module:   module,
		DepsDir:  depsDir,
		BuildDir: buildDir,
		Packages: make(map[string]*Package),
	}

	mainMod := filepath.Base(root)

	// Load packages from the main module
	if err := workspace.loadPackagesFromDir(root, mainMod); err != nil {
		return nil, fmt.Errorf("loading packages from root: %w", err)
	}

	// Load packages from dependencies
	if _, err := os.Stat(depsDir); err == nil {
		if err := workspace.loadDependencyPackages(); err != nil {
			return nil, fmt.Errorf("loading dependency packages: %w", err)
		}
	}

	return workspace, nil
}

// parseModuleFile parses a MODULE file and returns the module configuration
func parseModuleFile(filename string) (*ModuleFile, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	module := &ModuleFile{
		Dependencies: make(map[string]Dependency),
	}

	scanner := bufio.NewScanner(file)
	var currentDep *Dependency
	var currentDepName string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse key: value pairs
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}

			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			switch key {
			case "name":
				module.Name = value
			default:
				// Assume it's a dependency name
				currentDepName = key
				currentDep = &Dependency{URL: value}
				module.Dependencies[currentDepName] = *currentDep
			}
		} else if currentDep != nil && strings.Contains(line, "=") {
			// Parse dependency attributes
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				attr := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])

				switch attr {
				case "branch":
					currentDep.Branch = value
				case "tag":
					currentDep.Tag = value
				case "hash":
					currentDep.Hash = value
				}
				module.Dependencies[currentDepName] = *currentDep
			}
		}
	}

	return module, scanner.Err()
}

// loadPackagesFromDir loads Go packages from a directory tree
func (w *Workspace) loadPackagesFromDir(root, importPrefix string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			return nil
		}

		// Skip DEPS directory when scanning root
		if path == w.DepsDir || path == w.BuildDir {
			return filepath.SkipDir
		}

		// Skip hidden directories and common build artifacts
		if strings.HasPrefix(d.Name(), ".") ||
			d.Name() == "vendor" ||
			d.Name() == "node_modules" {
			return filepath.SkipDir
		}

		pkg, err := w.loadPackage(path, importPrefix)
		if err != nil {
			// Log error but continue walking
			return nil
		}

		if pkg != nil {
			w.Packages[pkg.ImportPath] = pkg
		}

		return nil
	})
}

// loadDependencyPackages loads packages from the DEPS directory
func (w *Workspace) loadDependencyPackages() error {
	entries, err := os.ReadDir(w.DepsDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		depDir := filepath.Join(w.DepsDir, entry.Name())
		depImportPrefix := entry.Name()

		if err := w.loadPackagesFromDir(depDir, depImportPrefix); err != nil {
			// Log error but continue with other dependencies
			continue
		}
	}

	return nil
}

// loadPackage loads a single Go package from a directory
func (w *Workspace) loadPackage(dir, importPrefix string) (*Package, error) {
	// Check if directory contains Go files
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var goFiles, testGoFiles []string
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".go") {
			continue
		}

		if strings.HasSuffix(file.Name(), "_test.go") {
			testGoFiles = append(testGoFiles, file.Name())
		} else {
			goFiles = append(goFiles, file.Name())
		}
	}

	if len(goFiles) == 0 && len(testGoFiles) == 0 {
		return nil, nil // No Go package
	}

	// Determine import path
	var importPath string
	if importPrefix == filepath.Base(w.Root) {
		// Main module - use relative path from root
		relPath, err := filepath.Rel(w.Root, dir)
		if err != nil {
			return nil, err
		}

		importPath = importPrefix + "/" + filepath.ToSlash(relPath)
	} else {
		// Dependency - use import prefix
		relPath, err := filepath.Rel(filepath.Join(w.DepsDir, importPrefix), dir)
		if err != nil {
			return nil, err
		}

		if relPath == "." {
			importPath = importPrefix
		} else {
			importPath = importPrefix + "/" + filepath.ToSlash(relPath)
		}
	}

	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	pkg := &Package{
		ID:          importPath,
		ImportPath:  importPath,
		Dir:         dirAbs,
		GoFiles:     goFiles,
		TestGoFiles: testGoFiles,
	}

	// Parse package name and imports
	if len(goFiles) > 0 {
		if err := w.parsePackageFiles(pkg, goFiles, false); err != nil {
			pkg.Errors = append(pkg.Errors, err)
		}
	}

	if len(testGoFiles) > 0 {
		if err := w.parsePackageFiles(pkg, testGoFiles, true); err != nil {
			pkg.Errors = append(pkg.Errors, err)
		}
	}

	return pkg, nil
}

// parsePackageFiles parses Go files to extract package name and imports
func (w *Workspace) parsePackageFiles(pkg *Package, files []string, isTest bool) error {
	fset := token.NewFileSet()

	for _, filename := range files {
		fullPath := filepath.Join(pkg.Dir, filename)
		src, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		file, err := parser.ParseFile(fset, fullPath, src, parser.ImportsOnly)
		if err != nil {
			continue
		}

		// Set package name from first non-test file
		if !isTest && pkg.Name == "" {
			pkg.Name = file.Name.Name
		}

		// Extract imports
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if isTest {
				pkg.TestImports = append(pkg.TestImports, path)
			} else {
				pkg.Imports = append(pkg.Imports, path)
			}
		}
	}

	// Remove duplicates and sort
	if !isTest {
		pkg.Imports = removeDuplicates(pkg.Imports)
		sort.Strings(pkg.Imports)
	} else {
		pkg.TestImports = removeDuplicates(pkg.TestImports)
		sort.Strings(pkg.TestImports)
	}

	return nil
}

// removeDuplicates removes duplicate strings from a slice
func removeDuplicates(slice []string) []string {
	seen := make(map[string]bool)
	var result []string

	for _, item := range slice {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}

	return result
}

// GetPackage returns a package by import path
func (w *Workspace) GetPackage(importPath string) *Package {
	return w.Packages[importPath]
}

// ListPackages returns all packages in the workspace
func (w *Workspace) ListPackages() []*Package {
	var packages []*Package
	for _, pkg := range w.Packages {
		packages = append(packages, pkg)
	}
	return packages
}
