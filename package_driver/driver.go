package package_driver

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/daedaleanai/dbt/v3/util"
	"github.com/daedaleanai/dbt/v3/workspace"

	"golang.org/x/tools/go/packages"
)

const dbtRulesDirName = "dbt-rules"

// Driver implements the go/packages driver protocol for dbt workspaces
type Driver struct {
	workspace *workspace.Workspace
}

// NewDriver creates a new dbt driver for the given directory
func NewDriver(dir string) (*Driver, error) {
	workspaceRoot := util.GetWorkspaceRoot()
	workspace, err := workspace.OpenWorkspace(workspaceRoot)
	if err != nil {
		return nil, err
	}

	return &Driver{workspace: workspace}, nil
}

// HandleRequest processes a go/packages driver request
func (d *Driver) HandleRequest(request *packages.DriverRequest, patterns []string) (*packages.DriverResponse, error) {
	// Find matching packages based on patterns
	matchedPackages, err := d.findPackages(patterns)
	if err != nil {
		return nil, fmt.Errorf("finding packages: %w", err)
	}

	if len(matchedPackages) == 0 {
		return &packages.DriverResponse{
			NotHandled: true,
		}, nil
	}

	// Convert to packages.Package format
	var pkgs []*packages.Package
	var roots []string

	for _, dbtPkg := range matchedPackages {
		pkg := d.convertPackage(dbtPkg, request.Mode)
		pkgs = append(pkgs, pkg)
		roots = append(roots, pkg.ID)
	}

	// Resolve imports between packages
	d.resolveImports(pkgs)

	response := &packages.DriverResponse{
		Roots:     roots,
		Packages:  pkgs,
		Compiler:  "",
		Arch:      "", // TODO: detect from environment
		GoVersion: 0,  // TODO: parse from go.mod or MODULE
	}
	return response, nil
}

// findPackages finds packages matching the given patterns
func (d *Driver) findPackages(patterns []string) ([]*Package, error) {
	var matched []*Package

	for _, pattern := range patterns {
		if pattern == "./..." {
			// Return all packages in workspace
			for _, pkg := range d.workspace.Packages {
				matched = append(matched, pkg)
			}
		} else if pattern == "." {
			// Return package in current directory
			wd, err := os.Getwd()
			if err != nil {
				return nil, err
			}

			for _, pkg := range d.workspace.Packages {
				if pkg.Dir == wd {
					matched = append(matched, pkg)
					break
				}
			}
		} else if strings.HasSuffix(pattern, "/...") {
			// Return packages under prefix
			prefix := strings.TrimSuffix(pattern, "/...")
			for _, pkg := range d.workspace.Packages {
				if pkg.ImportPath == prefix || strings.HasPrefix(pkg.ImportPath, prefix+"/") {
					matched = append(matched, pkg)
				}
			}
		} else {
			// Exact match
			if pkg := d.workspace.GetPackage(pattern); pkg != nil {
				matched = append(matched, pkg)
			}
		}
	}

	return matched, nil
}

// convertPackage converts a dbt Package to packages.Package
func (d *Driver) convertPackage(dbtPkg *Package, mode packages.LoadMode) *packages.Package {
	pkg := &packages.Package{
		ID:              "dbt@" + dbtPkg.ID,
		Name:            dbtPkg.Name,
		PkgPath:         dbtPkg.ImportPath,
		GoFiles:         make([]string, len(dbtPkg.GoFiles)),
		CompiledGoFiles: make([]string, len(dbtPkg.GoFiles)),
		OtherFiles:      []string{},
		Imports:         make(map[string]*packages.Package),
	}

	// Convert file paths to absolute
	for i, file := range dbtPkg.GoFiles {
		pkg.GoFiles[i] = filepath.Join(dbtPkg.Dir, file)
		pkg.CompiledGoFiles[i] = filepath.Join(dbtPkg.Dir, file)
	}

	// Add test files if requested
	// Note: packages.LoadTests is used in the config, not a mode flag
	// Test files are handled separately in the driver request

	// Handle errors
	if len(dbtPkg.Errors) > 0 {
		pkg.Errors = make([]packages.Error, len(dbtPkg.Errors))
		for i, err := range dbtPkg.Errors {
			pkg.Errors[i] = packages.Error{
				Pos:  "-",
				Msg:  err.Error(),
				Kind: packages.ParseError,
			}
		}
	}

	return pkg
}

// resolveImports creates stub packages for imports and links them
func (d *Driver) resolveImports(pkgs []*packages.Package) {
	// Create a map of all packages by import path
	packageMap := make(map[string]*packages.Package)
	for _, pkg := range pkgs {
		packageMap[pkg.PkgPath] = pkg
	}

	// Resolve imports for each package
	for _, pkg := range pkgs {
		// Find the corresponding dbt package
		dbtPkg := d.workspace.GetPackage(pkg.PkgPath)
		if dbtPkg == nil {
			continue
		}

		// Create import map
		allImports := append([]string{}, dbtPkg.Imports...)
		allImports = append(allImports, dbtPkg.TestImports...)

		for _, importPath := range allImports {
			if importPkg, exists := packageMap[importPath]; exists {
				// Link to existing package
				pkg.Imports[importPath] = importPkg
			} else {
				// Create stub package for external import
				pkg.Imports[importPath] = &packages.Package{
					ID:      importPath,
					PkgPath: importPath,
				}
			}
		}
	}
}

func logJson(msg string, value any) {
	serialized, err := json.Marshal(value)
	if err != nil {
		log.Fatalf("Unable to marshal as json: %v", err)
	}
	log.Printf("%s: %s", msg, string(serialized))
}

// Main entry point for the dbt driver executable
func Main() {
	file, err := os.OpenFile("/tmp/dbtdriver.log", os.O_APPEND|os.O_CREATE|os.O_RDWR, 0777)
	if err != nil {
		log.Fatal("Unable to create log file: ", err)
	}
	log.SetOutput(file)
	defer file.Close()

	// Parse command line arguments (patterns)
	patterns := os.Args[1:]

	// Read request from stdin
	var request packages.DriverRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		log.Fatalf("decoding driver request: %v", err)
	}

	// Determine working directory
	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("unable to get cwd: %v", err)
	}

	// Create driver
	driver, err := NewDriver(wd)
	if err != nil {
		// Not a dbt workspace, return NotHandled
		response := &packages.DriverResponse{
			NotHandled: true,
		}
		if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
			log.Fatalf("encoding response: %v", err)
		}
		logJson("Response: ", response)
		return
	}

	logJson("Request: ", request)

	// Handle the request
	response, err := driver.HandleRequest(&request, patterns)
	if err != nil {
		log.Fatalf("handling request: %v", err)
	}

	// Write response to stdout
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		log.Fatalf("encoding response: %v", err)
	}
	logJson("Response: ", response)
}
