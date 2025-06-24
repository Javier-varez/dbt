package workspace

import (
	"fmt"
	"fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/daedaleanai/dbt/v3/log"
	"github.com/daedaleanai/dbt/v3/module"
	"github.com/daedaleanai/dbt/v3/util"
)

// Dependency represents a dependency declaration in a MODULE file
type Dependency struct {
	ModuleFile *module.ModuleFile
	Module     module.Module
}

// Workspace represents a dbt workspace with its dependencies
type Workspace struct {
	Root       string
	ModuleFile *module.ModuleFile
	Module     module.Module
	Deps       map[string]*Dependency
	NeedsSync  bool
}

func (w *Workspace) collectDependencies() error {
	queue := []*module.ModuleFile{
		w.ModuleFile,
	}

	for len(queue) > 0 {
		top := queue[0]
		queue = queue[1:]

		for depName := range top.Dependencies {
			depPath := filepath.Join(w.Root, util.DepsDirName, depName)

			if !util.DirExists(depPath) {
				log.Warning("Dependency %s at %s is not sync'ed. Skipping it.\n", depName, depPath)
				w.NeedsSync = true
				continue
			}

			depModule := module.OpenModule(depPath)
			depModuleFile := module.ReadModuleFile(depPath)

			if _, ok := w.Deps[depName]; !ok {
				log.Debug("Adding dependency: %s dir: %s\n", depName, depPath)
				w.Deps[depName] = &Dependency{
					Module:     depModule,
					ModuleFile: &depModuleFile,
				}
				queue = append(queue, &depModuleFile)
			}
		}
	}

	return nil
}

func OpenWorkspace(dir string) (*Workspace, error) {
	workspaceRoot := util.GetWorkspaceRoot()
	log.Debug("Workspace: %s.\n", workspaceRoot)

	workspaceModuleFile := module.ReadModuleFile(workspaceRoot)
	workspaceModule := module.OpenModule(workspaceRoot)
	log.Debug("Workspace module name: '%s'\n", workspaceModule.Name())

	// Ensure DEPS/ directory exists, and warn if it seems to be mangled by the user.
	util.EnsureManagedDir(util.DepsDirName)

	w := &Workspace{
		Root:       workspaceRoot,
		ModuleFile: &workspaceModuleFile,
		Module:     workspaceModule,
		Deps:       make(map[string]*Dependency),
	}

	w.collectDependencies()
	return w, nil
}

func CollectFilesByPatternInMod(modName string, modDir string, pattern string) ([]string, error) {
	absModDir, err := filepath.Abs(modDir)
	if err != nil {
		return nil, err
	}

	var files []string
	err = filepath.WalkDir(absModDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			return nil
		}

		// Skipped dirs
		if strings.HasPrefix(d.Name(), ".") || d.Name() == util.DepsDirName || d.Name() == util.BuildDirName {
			return filepath.SkipDir
		}

		relativePath := filepath.Rel(absModDir, path)
		modName + filepath.ToSlash()

		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

func (w *Workspace) CollectBuildFiles() (map[string]string, error) {
	// Get go files for the top module
	rootModName := w.Module.Name()

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

// Lists all go packages
func (w *Workspace) GoPackages() []Package {
	// TODO
}
