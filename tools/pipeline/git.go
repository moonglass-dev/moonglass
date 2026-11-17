package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// env is what every command needs: the checkout it runs in and where to write.
type env struct {
	repo    string
	out     io.Writer
	noFetch bool
	forge   forge
	// runCI runs the full gate (make ci) in dir. Tests replace it.
	runCI func(dir string) (string, error)
	// sleep waits between merge polls. Tests replace it.
	sleep func(time.Duration)
}

func (e *env) pause(d time.Duration) {
	if e.sleep != nil {
		e.sleep(d)
		return
	}
	time.Sleep(d)
}

func (e *env) ci(dir string) (string, error) {
	if e.runCI != nil {
		return e.runCI(dir)
	}
	return runCmd(dir, "make", "ci")
}

func (e *env) gh() forge {
	if e.forge != nil {
		return e.forge
	}
	return ghForge{dir: e.repo}
}

// runCmd runs a fixed tool (git, make, go, golangci-lint, gh) and returns its
// combined output. Arguments are never passed through a shell.
func runCmd(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, tail(out, 40))
	}
	return out, nil
}

// git runs git and returns stdout only, trimmed.
func git(dir string, args ...string) (string, error) {
	out, err := gitRaw(dir, args...)
	return strings.TrimSpace(out), err
}

// gitRaw runs git and returns stdout exactly as printed.
func gitRaw(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func repoRoot(dir string) (string, error) {
	root, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not inside a git checkout of this repository: %w", err)
	}
	return root, nil
}

// mainWorktree is the checkout that owns .pipeline/, even when called from a
// linked worktree that T3 created for a ticket.
func mainWorktree(dir string) (string, error) {
	common, err := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), nil
	}
	return common, nil
}

func stopped(dir string) (bool, error) {
	main, err := mainWorktree(dir)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(filepath.Join(main, ".pipeline", "STOP"))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("read stop file: %w", err)
}

func requireRunning(dir string) error {
	stop, err := stopped(dir)
	if err != nil {
		return err
	}
	if stop {
		return errStop
	}
	return nil
}

func fetch(e *env) error {
	if e.noFetch {
		return nil
	}
	_, err := git(e.repo, "fetch", "--prune", "--quiet", "origin")
	if err != nil {
		return fmt.Errorf("fetch origin (check the network and gh auth): %w", err)
	}
	return nil
}

func refExists(dir, ref string) bool {
	_, err := git(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

func branchRef(id string) string { return "p/" + id }

func remoteBranchRef(id string) string { return "origin/p/" + id }

func currentBranch(dir string) (string, error) {
	return git(dir, "rev-parse", "--abbrev-ref", "HEAD")
}

func head(dir string) (string, error) { return git(dir, "rev-parse", "HEAD") }

func mergeBase(dir, a, b string) (string, error) { return git(dir, "merge-base", a, b) }

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// changedSince lists files that differ between from and to (two commits).
func changedSince(dir, from, to string) ([]string, error) {
	out, err := git(dir, "diff", "--name-only", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

// dirtyFiles lists uncommitted and untracked paths in the worktree.
func dirtyFiles(dir string) ([]string, error) {
	out, err := gitRaw(dir, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range strings.Split(out, "\x00") {
		if len(entry) > 3 {
			files = append(files, entry[3:])
		}
	}
	return files, nil
}

// tree reads files either from a checkout on disk or from a git ref.
type tree interface {
	read(path string) ([]byte, bool, error)
}

type fsTree struct{ root string }

func (t fsTree) read(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(filepath.Join(t.root, filepath.FromSlash(path)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

type refTree struct{ dir, ref string }

func (t refTree) read(path string) ([]byte, bool, error) {
	if _, err := git(t.dir, "cat-file", "-e", t.ref+":"+path); err != nil {
		return nil, false, nil
	}
	cmd := exec.Command("git", "show", t.ref+":"+path)
	cmd.Dir = t.dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, false, fmt.Errorf("git show %s:%s: %w: %s", t.ref, path, err, stderr.String())
	}
	return stdout.Bytes(), true, nil
}

func tail(s string, n int) string {
	ls := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n")
}

// commitAll stages everything and commits. It is a no-op when nothing changed.
func commitAll(dir, msg string) error {
	if _, err := git(dir, "add", "-A"); err != nil {
		return err
	}
	if _, err := git(dir, "diff", "--cached", "--quiet"); err == nil {
		return nil
	}
	_, err := git(dir, "commit", "--quiet", "-m", msg)
	return err
}

func push(dir, id string) error {
	_, err := git(dir, "push", "--quiet", "origin", "HEAD:refs/heads/"+branchRef(id))
	if err != nil {
		return fmt.Errorf("push %s (someone else pushed to it, or origin is unreachable): %w", branchRef(id), err)
	}
	return nil
}

// withBranchWorktree runs fn in a temporary detached worktree at origin/p/ID,
// so the dispatcher can change a ticket branch without touching its checkout.
func withBranchWorktree(e *env, id string, fn func(dir string) error) error {
	main, err := mainWorktree(e.repo)
	if err != nil {
		return err
	}
	dir := filepath.Join(main, ".pipeline", "tmp", id)
	_, _ = git(e.repo, "worktree", "remove", "--force", dir)
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dir), err)
	}
	if _, err := git(e.repo, "worktree", "add", "--quiet", "--detach", dir, remoteBranchRef(id)); err != nil {
		return err
	}
	defer func() { _, _ = git(e.repo, "worktree", "remove", "--force", dir) }()
	return fn(dir)
}

// inTicketCheckout runs fn in the current checkout when it is on p/ID,
// otherwise in a temporary worktree of origin/p/ID.
func inTicketCheckout(e *env, id string, fn func(dir string) error) error {
	branch, err := currentBranch(e.repo)
	if err != nil {
		return err
	}
	if branch == branchRef(id) {
		return fn(e.repo)
	}
	if err := fetch(e); err != nil {
		return err
	}
	if !refExists(e.repo, remoteBranchRef(id)) {
		return fmt.Errorf("ticket %s has no branch %s on origin: start its worker first", id, branchRef(id))
	}
	return withBranchWorktree(e, id, fn)
}
