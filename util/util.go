package util

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/daedaleanai/dbt/log"
	"gopkg.in/yaml.v2"
)

// DbtVersion is the current version of DBT. The minor version
// is also used as the MODULE file version.
var DbtVersion = [3]uint{1, 3, 14}

// ModuleFileName is the name of the file describing each module.
const (
	ModuleFileName = "MODULE"
	BuildDirName   = "BUILD"
	// DepsDirName is directory that dependencies are stored in.
	DepsDirName = "DEPS"
)

const fileMode = 0664
const dirMode = 0775

// Reimplementation of CutPrefix for backwards compatibility with versions < 1.20
func CutPrefix(str string, prefix string) (string, bool) {
	if strings.HasPrefix(str, prefix) {
		return str[len(prefix):], true
	}
	return str, false
}

// FileExists checks whether some file exists.
func FileExists(file string) bool {
	stat, err := os.Stat(file)
	return err == nil && !stat.IsDir()
}

// DirExists checks whether some directory exists.
func DirExists(dir string) bool {
	stat, err := os.Stat(dir)
	return err == nil && stat.IsDir()
}

// RemoveDir removes a directory and all of its content.
func RemoveDir(p string) {
	err := os.RemoveAll(p)
	if err != nil {
		log.Fatal("Failed to remove directory '%s': %s.\n", p, err)
	}
}

// MkdirAll creates directory `p` and all parent directories.
func MkdirAll(p string) {
	err := os.MkdirAll(p, dirMode)
	if err != nil {
		log.Fatal("Failed to create directory '%s': %s.\n", p, err)
	}
}

func ReadFile(filePath string) []byte {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatal("Failed to read file '%s': %s.\n", filePath, err)
	}
	return data
}

func ReadJson(filePath string, v interface{}) {
	err := json.Unmarshal(ReadFile(filePath), v)
	if err != nil {
		log.Fatal("Failed to unmarshal JSON file '%s': %s.\n", filePath, err)
	}
}

func ReadYaml(filePath string, v interface{}) {
	err := yaml.Unmarshal(ReadFile(filePath), v)
	if err != nil {
		log.Fatal("Failed to unmarshal YAML file '%s': %s.\n", filePath, err)
	}
}

func WriteFile(filePath string, data []byte) {
	dir := path.Dir(filePath)
	err := os.MkdirAll(dir, dirMode)
	if err != nil {
		log.Fatal("Failed to create directory '%s': %s.\n", dir, err)
	}
	err = ioutil.WriteFile(filePath, data, fileMode)
	if err != nil {
		log.Fatal("Failed to write file '%s': %s.\n", filePath, err)
	}
}

func WriteJson(filePath string, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Fatal("Failed to marshal JSON for file '%s': %s.\n", filePath, err)
	}
	WriteFile(filePath, data)
}

func WriteYaml(filePath string, v interface{}) {
	data, err := yaml.Marshal(v)
	if err != nil {
		log.Fatal("Failed to marshal YAML for file '%s': %s.\n", filePath, err)
	}
	WriteFile(filePath, data)
}

func CopyFile(sourceFile, destFile string) {
	WriteFile(destFile, ReadFile(sourceFile))
}

// Copies a directory recursing into its inner directories
func CopyDirRecursively(sourceDir, destDir string) error {
	var wg sync.WaitGroup
	err := copyDirRecursivelyInner(sourceDir, destDir, &wg)
	wg.Wait()

	return err
}

func copyDirRecursivelyInner(sourceDir, destDir string, wg *sync.WaitGroup) error {
	stat, err := os.Stat(sourceDir)
	if err != nil {
		return err
	}

	if !stat.IsDir() {
		return fmt.Errorf("'%s' is not a directory", sourceDir)
	}

	MkdirAll(destDir)

	fileInfos, err := ioutil.ReadDir(sourceDir)
	for _, fileInfo := range fileInfos {
		if fileInfo.IsDir() {
			err = copyDirRecursivelyInner(path.Join(sourceDir, fileInfo.Name()), path.Join(destDir, fileInfo.Name()), wg)
			if err != nil {
				return err
			}

			if err := os.Chmod(path.Join(destDir, fileInfo.Name()), fileInfo.Mode()); err != nil {
				return err
			}
		} else {
			wg.Add(1)

			go func(source, dest string, sourceFileInfo os.FileInfo) {
				defer wg.Done()
				CopyFile(source, dest)
				if err := os.Chmod(dest, sourceFileInfo.Mode()); err != nil {
					log.Fatal("failed to change filemode: ", err)
				}
			}(path.Join(sourceDir, fileInfo.Name()), path.Join(destDir, fileInfo.Name()), fileInfo)
		}
	}

	return nil
}

func getModuleRoot(p string) (string, error) {
	for {
		moduleFilePath := path.Join(p, ModuleFileName)
		gitDirPath := path.Join(p, ".git")
		parentDirName := path.Base(path.Dir(p))
		if FileExists(moduleFilePath) || parentDirName == DepsDirName || DirExists(gitDirPath) {
			return p, nil
		}
		if p == "/" {
			return "", fmt.Errorf("not inside a module")
		}
		p = path.Dir(p)
	}
}

func GetModuleRootForPath(p string) string {
	moduleRoot, err := getModuleRoot(p)
	if err != nil {
		log.Fatal("Could not identify module root directory. Make sure you run this command inside a module: %s.\n", err)
	}
	return moduleRoot
}

// GetModuleRoot returns the root directory of the current module.
func GetModuleRoot() string {
	workingDir := GetWorkingDir()
	moduleRoot, err := getModuleRoot(workingDir)
	if err != nil {
		log.Fatal("Could not identify module root directory. Make sure you run this command inside a module: %s.\n", err)
	}
	return moduleRoot
}

// GetWorkspaceRoot returns the root directory of the current workspace (i.e., top-level module).
func GetWorkspaceRoot() string {
	var err error
	p := GetWorkingDir()
	for {
		p, err = getModuleRoot(p)
		if err != nil {
			log.Fatal("Could not identify workspace root directory. Make sure you run this command inside a workspace: %s.\n", err)
		}

		parentDirName := path.Base(path.Dir(p))
		if parentDirName != DepsDirName {
			return p
		}
		p = path.Dir(p)
	}
}

// GetWorkingDir returns the current working directory.
func GetWorkingDir() string {
	workingDir, err := os.Getwd()
	if err != nil {
		log.Fatal("Could not get working directory: %s.\n", err)
	}
	return workingDir
}

// WalkSymlink works like filepath.Walk but also accepts symbolic links as `root`.
func WalkSymlink(root string, walkFn filepath.WalkFunc) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}

	if (info.Mode() & os.ModeSymlink) != os.ModeSymlink {
		return filepath.Walk(root, walkFn)
	}

	link, err := os.Readlink(root)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(link) {
		link = path.Join(path.Dir(root), link)
	}

	return filepath.Walk(link, func(file string, info os.FileInfo, err error) error {
		file = path.Join(root, strings.TrimPrefix(file, link))
		return walkFn(file, info, err)
	})
}

func CheckManagedDirs() {
	workspaceRoot := GetWorkspaceRoot()
	checkManagedDir(workspaceRoot, BuildDirName)
	checkManagedDir(workspaceRoot, DepsDirName)
}

func checkManagedDir(root, child string) {
	dir := filepath.Join(root, child)
	stat, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.Fatal("Failed to stat directory %s: %v\n", dir, err)
	}
	if (stat.Mode() & os.ModeSymlink) != 0 {
		log.Fatal("%s special directory must not be a symlink\n", child)
	}
	if !stat.IsDir() {
		log.Fatal("Workspace contains file %s, which overlaps with a special purpose directory used by dbt\n", child)
	}
}

//go:embed WARNING.readme.txt
var warningText string

func EnsureManagedDir(dir string) {
	workspaceRoot := GetWorkspaceRoot()
	checkManagedDir(workspaceRoot, dir)

	if err := os.MkdirAll(filepath.Join(workspaceRoot, dir), dirMode); err != nil {
		log.Fatal("Failed to create special directory %s: %v\n", dir, err)
	}

	warningFilepath := filepath.Join(workspaceRoot, dir, "WARNING.readme.txt")
	if _, err := os.Stat(warningFilepath); errors.Is(err, os.ErrNotExist) {
		// best effort, ignore errors
		os.WriteFile(warningFilepath, []byte(warningText), fileMode)
	}
}
