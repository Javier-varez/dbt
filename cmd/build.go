package cmd

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/daedaleanai/dbt/log"
	"github.com/daedaleanai/dbt/module"
	"github.com/daedaleanai/dbt/util"

	"github.com/daedaleanai/cobra"
)

const bashFileName = "build.sh"
const buildDirName = "BUILD"
const buildDirNamePrefix = "OUTPUT"
const buildFileName = "BUILD.go"
const dbtRulesDirName = "dbt-rules"
const generatorDirName = "GENERATOR"
const generatorOutputFileName = "output.json"
const initFileName = "init.go"
const mainFileName = "main.go"
const modFileName = "go.mod"
const ninjaFileName = "build.ninja"
const rulesDirName = "RULES"

const goMajorVersion = 1
const goMinorVersion = 16

const initFileTemplate = `
// This file is generated. Do not edit this file.

package %s

import "dbt-rules/RULES/core"

type __internal_pkg struct{}

func DbtMain(vars map[string]interface{}) {
%s
}

func in(name string) core.Path {
	return core.NewInPath(__internal_pkg{}, name)
}

func ins(names ...string) []core.Path {
	var paths []core.Path
	for _, name := range names {
		paths = append(paths, in(name))
	}
	return paths
}

func out(name string) core.OutPath {
	return core.NewOutPath(__internal_pkg{}, name)
}
`
const mainFileTemplate = `
// This file is generated. Do not edit this file.

package main

import (
	"regexp"
	"runtime"
	"strconv"
	
	"dbt-rules/RULES/core"
)

%s

func init() {
	requiredMajor := uint64(%d)
	requiredMinor := uint64(%d)

	re := regexp.MustCompile("^go([[:digit:]]+)\\.([[:digit:]]+)$")
	matches := re.FindStringSubmatch(runtime.Version())
	if matches == nil {
		core.Fatal("Failed to determine go version")
	}
	currentMajor, _ := strconv.ParseUint(matches[1], 10, 64)
	currentMinor, _ := strconv.ParseUint(matches[2], 10, 64)

	if currentMajor < requiredMajor || (currentMajor == requiredMajor && currentMinor < requiredMinor) {
		core.Fatal("DBT requires go version >= %%d.%%d. Found %%d.%%d", requiredMajor, requiredMinor, currentMajor, currentMinor)
	}
}

func main() {
    vars := map[string]interface{}{}

%s

    core.GeneratorMain(vars)
}
`

type target struct {
	Description string
}

type flag struct {
	Description   string
	Type          string
	AllowedValues []string
	Value         string
}

type generatorOutput struct {
	NinjaFile string
	BashFile  string
	Targets   map[string]target
	Flags     map[string]flag
	BuildDir  string
}

var buildCmd = &cobra.Command{
	Use:                   "build [targets] [build flags]",
	Short:                 "Builds the targets",
	Long:                  `Builds the targets.`,
	Run:                   runBuild,
	ValidArgsFunction:     completeBuildArgs,
	DisableFlagsInUseLine: true,
}

func init() {
	rootCmd.AddCommand(buildCmd)
}

func runBuild(cmd *cobra.Command, args []string) {
	dbtRulesDir := path.Join(util.GetWorkspaceRoot(), util.DepsDirName, dbtRulesDirName)
	if !util.DirExists(dbtRulesDir) {
		log.Fatal("You are running 'dbt build' without '%s' being available. Add that dependency, run 'dbt sync' and try again.\n", dbtRulesDirName)
		return
	}

	targets, flags := parseArgs(args)
	genOutput := runGenerator("buildFiles", flags)

	// Write the build files.
	ninjaFilePath := path.Join(genOutput.BuildDir, ninjaFileName)
	util.WriteFile(ninjaFilePath, []byte(genOutput.NinjaFile))

	bashFilePath := path.Join(genOutput.BuildDir, bashFileName)
	util.WriteFile(bashFilePath, []byte(genOutput.BashFile))

	log.Debug("Targets: '%s'.\n", strings.Join(targets, "', '"))

	// Get all available targets and flags.
	if len(targets) == 0 {
		log.Debug("No targets specified.\n")

		targetNames := []string{}
		for name := range genOutput.Targets {
			targetNames = append(targetNames, name)
		}
		sort.Strings(targetNames)

		fmt.Println("\nAvailable targets:")
		for _, name := range targetNames {
			target := genOutput.Targets[name]
			fmt.Printf("  //%s", name)
			if target.Description != "" {
				fmt.Printf("  //%s (%s)", name, target.Description)
			}
			fmt.Println()
		}

		fmt.Println("\nAvailable flags:")
		for name, flag := range genOutput.Flags {
			fmt.Printf("  %s='%s' [%s]", name, flag.Value, flag.Type)
			if len(flag.AllowedValues) > 0 {
				fmt.Printf(" ('%s')", strings.Join(flag.AllowedValues, "', '"))
			}
			if flag.Description != "" {
				fmt.Printf(" // %s", flag.Description)
			}
			fmt.Println()
		}
		return
	}

	expandedTargets := map[string]struct{}{}
	for _, target := range targets {
		if !strings.HasSuffix(target, "...") {
			if _, exists := genOutput.Targets[target]; !exists {
				log.Fatal("Target '%s' does not exist.\n", target)
			}
			expandedTargets[target] = struct{}{}
			continue
		}

		targetPrefix := strings.TrimSuffix(target, "...")
		found := false
		for availableTarget := range genOutput.Targets {
			if strings.HasPrefix(availableTarget, targetPrefix) {
				found = true
				expandedTargets[availableTarget] = struct{}{}
			}
		}
		if !found {
			log.Fatal("No target is matching pattern '%s'.\n", target)
		}
	}

	// Run ninja.
	ninjaArgs := []string{}
	if log.Verbose {
		ninjaArgs = append(ninjaArgs, "-v", "-d", "explain")
	}
	for target := range expandedTargets {
		ninjaArgs = append(ninjaArgs, target)
	}

	log.Debug("Running ninja command: 'ninja %s'\n", strings.Join(ninjaArgs, " "))
	ninjaCmd := exec.Command("ninja", ninjaArgs...)
	ninjaCmd.Dir = genOutput.BuildDir
	ninjaCmd.Stderr = os.Stderr
	ninjaCmd.Stdout = os.Stdout
	err := ninjaCmd.Run()
	if err != nil {
		log.Fatal("Running ninja failed: %s\n", err)
	}
}

func completeBuildArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	genOutput := runGenerator("completion", []string{})

	if strings.Contains(toComplete, "=") {
		suggestions := []string{}
		flag := strings.SplitN(toComplete, "=", 2)[0]
		for _, value := range genOutput.Flags[flag].AllowedValues {
			suggestions = append(suggestions, fmt.Sprintf("%s=%s", flag, value))
		}
		return suggestions, cobra.ShellCompDirectiveNoFileComp
	}

	suggestions := []string{}
	targetToComplete := normalizeTarget(toComplete)
	for name, target := range genOutput.Targets {
		if strings.HasPrefix(name, targetToComplete) {
			suggestions = append(suggestions, fmt.Sprintf("%s%s\t%s", toComplete, strings.TrimPrefix(name, targetToComplete), target.Description))
		}
	}

	for name, flag := range genOutput.Flags {
		suggestions = append(suggestions, fmt.Sprintf("%s=\t%s", name, flag.Description))
	}

	return suggestions, cobra.ShellCompDirectiveNoFileComp
}

func parseArgs(args []string) ([]string, []string) {
	targets := []string{}
	flags := []string{}

	// Split all args into two categories: If they contain a "= they are considered
	// build flags, otherwise a target to be built.
	for _, arg := range args {
		if strings.Contains(arg, "=") {
			flags = append(flags, arg)
		} else {
			targets = append(targets, normalizeTarget(arg))
		}
	}

	return targets, flags
}

func normalizeTarget(target string) string {
	// Build targets are interpreted as relative to the workspace root when they start with '//'.
	// Otherwise they are interpreted as relative to the current working directory.
	// E.g.: Running 'dbt build //src/path/to/mylib.a' from anywhere in the workspace is equivalent
	// to 'dbt build mylib.a' in '.../src/path/to/' or 'dbt build path/to/mylib.a' in '.../src/'.
	if strings.HasPrefix(target, "//") {
		return strings.TrimLeft(target, "/")
	}
	endsWithSlash := strings.HasSuffix(target, "/") || target == ""
	target = path.Join(util.GetWorkingDir(), target)
	moduleRoot := util.GetModuleRootForPath(target)
	target = strings.TrimPrefix(target, path.Dir(moduleRoot))
	if endsWithSlash {
		target = target + "/"
	}
	return strings.TrimLeft(target, "/")
}

func runGenerator(mode string, flags []string) generatorOutput {
	workspaceRoot := util.GetWorkspaceRoot()
	sourceDir := path.Join(workspaceRoot, util.DepsDirName)
	workingDir := util.GetWorkingDir()
	generatorDir := path.Join(workspaceRoot, buildDirName, generatorDirName)
	buildDirPrefix := path.Join(workspaceRoot, buildDirName, buildDirNamePrefix)

	// Remove all existing buildfiles.
	util.RemoveDir(generatorDir)

	// Copy all BUILD.go files and RULES/ files from the source directory.
	modules := module.GetAllModulePaths(workspaceRoot)
	packages := []string{}
	for modName, modPath := range modules {
		modBuildfilesDir := path.Join(generatorDir, modName)
		modulePackages := copyBuildAndRuleFiles(modName, modPath, modBuildfilesDir, modules)
		packages = append(packages, modulePackages...)
	}

	createGeneratorMainFile(generatorDir, packages, modules)
	createSumGoFile(generatorDir)

	cmdArgs := append([]string{"run", mainFileName, mode, sourceDir, buildDirPrefix, workingDir}, flags...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = generatorDir
	if mode != "completion" {
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
	}
	err := cmd.Run()
	if err != nil {
		log.Fatal("Failed to run generator: %s.\n", err)
	}
	var output generatorOutput
	generatorOutputPath := path.Join(generatorDir, generatorOutputFileName)
	util.ReadJson(generatorOutputPath, &output)
	return output
}

func copyBuildAndRuleFiles(moduleName, modulePath, buildFilesDir string, modules map[string]string) []string {
	packages := []string{}

	log.Debug("Processing module '%s'.\n", moduleName)

	modFileContent := createModFileContent(moduleName, modules, "..")
	util.WriteFile(path.Join(buildFilesDir, modFileName), modFileContent)

	buildFiles := []string{}
	err := util.WalkSymlink(modulePath, func(filePath string, file os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relativeFilePath := strings.TrimPrefix(filePath, modulePath+"/")

		// Ignore the BUILD/, DEPS/ and RULES/ directories.
		if file.IsDir() && (relativeFilePath == buildDirName || relativeFilePath == util.DepsDirName || relativeFilePath == rulesDirName) {
			return filepath.SkipDir
		}

		// Skip everything that is not a BUILD.go file.
		if file.IsDir() || file.Name() != buildFileName {
			return nil
		}

		log.Debug("Found %s file '%s'.\n", buildFileName, path.Join(modulePath, relativeFilePath))
		buildFiles = append(buildFiles, filePath)
		return nil
	})

	if err != nil {
		log.Fatal("Failed to process %s files for module %s: %s.\n", buildFileName, moduleName, err)
	}

	for _, buildFile := range buildFiles {
		relativeFilePath := strings.TrimPrefix(buildFile, modulePath+"/")
		relativeDirPath := strings.TrimSuffix(path.Dir(relativeFilePath), "/")

		packages = append(packages, path.Join(moduleName, relativeDirPath))
		packageName, vars := parseBuildFile(buildFile)
		varLines := []string{}
		for _, varName := range vars {
			varLines = append(varLines, fmt.Sprintf("    vars[in(\"%s\").Relative()] = &%s", varName, varName))
		}

		initFileContent := fmt.Sprintf(initFileTemplate, packageName, strings.Join(varLines, "\n"))
		initFilePath := path.Join(buildFilesDir, relativeDirPath, initFileName)
		util.WriteFile(initFilePath, []byte(initFileContent))

		copyFilePath := path.Join(buildFilesDir, relativeFilePath)
		util.CopyFile(buildFile, copyFilePath)
	}

	rulesDirPath := path.Join(modulePath, rulesDirName)
	if !util.DirExists(rulesDirPath) {
		return packages
	}

	err = filepath.Walk(rulesDirPath, func(filePath string, file os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if file.IsDir() || path.Ext(file.Name()) != ".go" {
			return nil
		}

		relativeFilePath := strings.TrimPrefix(filePath, modulePath+"/")
		copyFilePath := path.Join(buildFilesDir, relativeFilePath)
		util.CopyFile(filePath, copyFilePath)
		return nil
	})

	if err != nil {
		log.Fatal("Failed to process %s/ files for module '%s': %s.\n", rulesDirName, moduleName, err)
	}

	return packages
}

func parseBuildFile(buildFilePath string) (string, []string) {
	fileAst, err := parser.ParseFile(token.NewFileSet(), buildFilePath, nil, parser.AllErrors)

	if err != nil {
		log.Fatal("Failed to parse '%s': %s.\n", buildFilePath, err)
	}

	vars := []string{}

	for _, decl := range fileAst.Decls {
		decl, ok := decl.(*ast.GenDecl)
		if !ok {
			log.Fatal("'%s' contains invalid declarations. Only import statements and 'var' declarations are allowed.\n", buildFilePath)
		}

		for _, spec := range decl.Specs {
			switch spec := spec.(type) {
			case *ast.ImportSpec:
			case *ast.ValueSpec:
				if decl.Tok.String() != "var" {
					log.Fatal("'%s' contains invalid declarations. Only import statements and 'var' declarations are allowed.\n", buildFilePath)
				}
				for _, id := range spec.Names {
					if id.Name == "_" {
						log.Warning("'%s' contains an anonymous declarations.\n", buildFilePath)
						continue
					}
					vars = append(vars, id.Name)
				}
			default:
				log.Fatal("'%s' contains invalid declarations. Only import statements and 'var' declarations are allowed.\n", buildFilePath)
			}
		}
	}

	return fileAst.Name.String(), vars
}

func createModFileContent(moduleName string, modules map[string]string, pathPrefix string) []byte {
	mod := strings.Builder{}

	fmt.Fprintf(&mod, "module %s\n\n", moduleName)
	fmt.Fprintf(&mod, "go %d.%d\n\n", goMajorVersion, goMinorVersion)

	for modName := range modules {
		fmt.Fprintf(&mod, "require %s v0.0.0\n", modName)
		fmt.Fprintf(&mod, "replace %s => %s/%s\n\n", modName, pathPrefix, modName)
	}

	return []byte(mod.String())
}

func createGeneratorMainFile(generatorDir string, packages []string, modules map[string]string) {
	importLines := []string{}
	dbtMainLines := []string{}
	for idx, pkg := range packages {
		importLines = append(importLines, fmt.Sprintf("import p%d \"%s\"", idx, pkg))
		dbtMainLines = append(dbtMainLines, fmt.Sprintf("    p%d.DbtMain(vars)", idx))
	}

	mainFilePath := path.Join(generatorDir, mainFileName)
	mainFileContent := fmt.Sprintf(mainFileTemplate, strings.Join(importLines, "\n"), goMajorVersion, goMinorVersion, strings.Join(dbtMainLines, "\n"))
	util.WriteFile(mainFilePath, []byte(mainFileContent))

	modFilePath := path.Join(generatorDir, modFileName)
	modFileContent := createModFileContent("root", modules, ".")
	util.WriteFile(modFilePath, modFileContent)
}

func createSumGoFile(generatorDir string) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("go", "mod", "download")
	cmd.Dir = generatorDir
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	err := cmd.Run()
	fmt.Print(string(stderr.Bytes()))
	if err != nil {
		log.Fatal("Failed to run 'go mod download': %s.\n", err)
	}
}
