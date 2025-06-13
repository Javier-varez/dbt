package module

import (
	"bytes"
	"os/exec"
	"path"
	"strings"

	"github.com/daedaleanai/dbt/v3/log"
	"github.com/daedaleanai/dbt/v3/util"
)

// JujutsuModule is a module backed by a git repository.
type JujutsuModule struct {
	path string
}

// createJujutsuModule creates a new JujutsuModule in the given `modulePath`
// by cloning the repository from `url`. Uses a git backend
func createJujutsuModule(modulePath, url string) (Module, error) {
	mod := JujutsuModule{modulePath}
	util.MkdirAll(modulePath)
	if err := mod.clone(url); err != nil {
		return nil, err
	}

	return mod, nil
}

func (m JujutsuModule) Name() string {
	return strings.TrimSuffix(path.Base(m.URL()), ".git")
}

func (m JujutsuModule) RootPath() string {
	return m.path
}

// URL returns the url of the underlying git repository.
func (m JujutsuModule) URL() string {
	return m.runGitCommand("config", "--get", "remote.origin.url")
}

// Head returns the commit hash of the HEAD of the underlying git repository.
func (m JujutsuModule) Head() string {
	return m.RevParse("HEAD")
}

// RevParse returns the commit hash for the commit referenced by `ref`.
func (m JujutsuModule) RevParse(ref string) string {
	return string(m.runGitCommand("rev-list", "-n", "1", ref))
}

// IsDirty returns whether the underlying repository has any uncommited changes.
func (m JujutsuModule) IsDirty() bool {
	// jujutsu is never dirty by design, for better or worse
	return false
}

// IsAncestor returns whether ancestor is an ancestor of rev in the commit tree.
func (m JujutsuModule) IsAncestor(ancestor, rev string) bool {
	_, _, err := m.tryRunGitCommand("merge-base", "--is-ancestor", ancestor, rev)
	return err == nil
}

// Fetch fetches changes from the default remote and reports whether any updates have been fetched.
func (m JujutsuModule) Fetch() bool {
	if m.IsDirty() {
		// If the module has uncommited changes, it does not match any version.
		log.Warning("The module has uncommited changes. Not fetching any changes.\n")
		return false
	}

	return len(m.runJjCommand("git", "fetch")) > 0
}

// Checkout changes the current module's version to `ref`.
func (m JujutsuModule) Checkout(ref string) {
	// Checkout is unsupported in jj as of now. Let's disallow it.
	log.Fatal("Unable to checkout a JJ repository to ", ref)
}

func (m JujutsuModule) Type() ModuleType {
	return JujutsuModuleType
}

// GetMergeBase returns the best common ancestor that could be used for a merge between the two given references.
func (m JujutsuModule) GetMergeBase(revA, revB string) (string, error) {
	stdout, _, err := m.tryRunGitCommand("merge-base", revA, revB)
	return stdout, err
}

func (m JujutsuModule) GetCommitTitle(revision string) (string, error) {
	stdout, _, err := m.tryRunGitCommand("show", "--format=format:\"%s\"", "-s", revision)
	return stdout, err
}

func (m JujutsuModule) GetCommitAuthorName(revision string) (string, error) {
	stdout, _, err := m.tryRunGitCommand("show", "--format=format:\"%an\"", "-s", revision)
	return stdout, err
}

func (m JujutsuModule) GetCommitsBetweenRefs(base, head string) ([]string, error) {
	result := []string{}
	stdout, _, err := m.tryRunGitCommand("rev-list", strings.Join([]string{strings.TrimSpace(base), strings.TrimSpace(head)}, ".."))
	for _, line := range strings.Split(stdout, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if len(trimmedLine) == 0 {
			continue
		}

		result = append(result, trimmedLine)
	}
	return result, err
}

// Runs a jj command with the specified arguments, exiting with an error message if the command
// could not be executed
func (m JujutsuModule) runJjCommand(args ...string) string {
	stdout, stderr, err := m.tryRunJjCommand(args...)
	if err != nil {
		log.Fatal("Failed to run jj command 'jj %s':\n%s\n%s\n%s\n", strings.Join(args, " "), stderr, stdout, err)
	}
	return stdout
}

// Tries to run a jj subcommand and return stdout, stderr and an error if the process exited with
// an exit code != 0
func (m JujutsuModule) tryRunJjCommand(args ...string) (string, string, error) {
	stderr := bytes.Buffer{}
	stdout := bytes.Buffer{}
	log.Debug("Running jj command: jj %s\n", strings.Join(args, " "))
	cmd := exec.Command("jj", args...)
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	cmd.Dir = m.path
	err := cmd.Run()
	return strings.TrimSuffix(stdout.String(), "\n"), strings.TrimSuffix(stderr.String(), "\n"), err
}

// Runs a git command with the specified arguments, exiting with an error message if the command
// could not be executed. Finds the location of the underlying git repo for this jj repo.
func (m JujutsuModule) runGitCommand(args ...string) string {
	stdout, stderr, err := m.tryRunGitCommand(args...)
	if err != nil {
		log.Fatal("Failed to run git command 'git %s':\n%s\n%s\n%s\n", strings.Join(args, " "), stderr, stdout, err)
	}
	return stdout
}

// Tries to run a git subcommand and return stdout, stderr and an error if the process exited with
// an exit code != 0. Finds the location of the underlying git repo for this jj repo.
func (m JujutsuModule) tryRunGitCommand(args ...string) (string, string, error) {
	stderr := bytes.Buffer{}
	stdout := bytes.Buffer{}
	log.Debug("Running git command: git %s\n", strings.Join(args, " "))
	cmd := exec.Command("git", args...)
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	gitRepoPath := m.locateGitRepo()
	cmd.Dir = gitRepoPath
	err := cmd.Run()
	return strings.TrimSuffix(stdout.String(), "\n"), strings.TrimSuffix(stderr.String(), "\n"), err
}

func (m JujutsuModule) locateGitRepo() string {
	return m.runJjCommand("git", "root")
}

// Clones a module from the given url at the specfied path location.
func (m JujutsuModule) clone(url string) error {
	var err error
	log.Log("Cloning '%s'.\n", url)
	_, _, err = m.tryRunJjCommand("git", "clone", url, m.path)
	if err != nil {
		// Leave clean state so that the operation can be retried
		util.RemoveDir(m.path)
	}
	return err
}
