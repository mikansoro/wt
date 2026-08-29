package integration

// Black-box integration tests for the wt CLI. Every test execs the compiled binary and
// asserts on stdout, stderr, exit codes, and on-disk/git state; none of them call wt's
// internal functions directly.
//
// The interactive overwrite prompt's "y" (confirm and overwrite) path is not exercised here:
// driving it needs a real pty, and this suite is stdlib-only with no pty support. Running wt
// with Setsid (no controlling terminal) instead covers the corresponding refusal branch,
// where /dev/tty can't be opened and wt must abort with exit 2 rather than silently overwrite.
// The --yes flag takes the same code path as a typed "y", so the proceed branch is covered
// through it (TestGoYesOverwritesDirtySlot, TestReleaseDirtySlot, and friends).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

var (
	wtBinary   string
	sharedHome string
)

func TestMain(m *testing.M) {
	os.Exit(runTestSuite(m))
}

// runTestSuite builds the wt binary once into a scratch directory, points every subprocess
// at an isolated HOME, runs the suite, and cleans up. It returns an exit code rather than
// calling os.Exit directly so its deferred cleanup always runs.
func runTestSuite(m *testing.M) int {
	buildDir, err := os.MkdirTemp("", "wt-build-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "wt test harness: creating build temp dir: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(buildDir) }()

	home, err := os.MkdirTemp("", "wt-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "wt test harness: creating home temp dir: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(home) }()
	sharedHome = home

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wt test harness: getting working dir: %v\n", err)
		return 1
	}
	moduleRoot := filepath.Dir(cwd)

	wtBinary = filepath.Join(buildDir, "wt")

	build := exec.Command("go", "build", "-o", wtBinary, "./cmd/wt")
	build.Dir = moduleRoot
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "wt test harness: building wt binary failed: %v\n%s\n", err, out)
		return 1
	}

	return m.Run()
}

// testEnv is the isolated environment every wt and git subprocess runs with: a scratch HOME,
// no global/system git config, a fixed commit identity, and the parent PATH so "git" resolves.
func testEnv() []string {
	return []string{
		"HOME=" + sharedHome,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
		"PATH=" + os.Getenv("PATH"),
	}
}

// runWT execs the compiled wt binary with dir as its working directory and no controlling
// terminal (Setsid), so any destructive prompt's attempt to open /dev/tty fails deterministically
// and wt aborts with exit 2 instead of blocking on a read that will never be answered.
func runWT(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	cmd := exec.Command(wtBinary, args...)
	cmd.Dir = dir
	cmd.Env = testEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode()
		}
		t.Fatalf("running wt %v: %v", args, err)
	}

	return outBuf.String(), errBuf.String(), 0
}

// runGit execs the real git binary for fixture setup and for ground-truth assertions.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = testEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v (dir=%s): %v\nstderr: %s", args, dir, err, stderr.String())
	}

	return strings.TrimRight(stdout.String(), "\n")
}

// resolvePath normalizes a path through any symlinks so paths built independently, such as
// wt's own stdout versus a path built by joining strings in a test, compare equal even if
// some ancestor directory (a platform tmp dir, for instance) is itself a symlink.
func resolvePath(t *testing.T, p string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return resolved
}

// assertSingleLinePath checks the §6.2 output contract: wt go must print the target slot's
// absolute path as the only content on stdout, with at most one trailing newline.
func assertSingleLinePath(t *testing.T, stdout string) string {
	t.Helper()

	trimmed := strings.TrimRight(stdout, "\n")
	if trimmed == "" {
		t.Fatalf("stdout was empty, want an absolute slot path")
	}
	if strings.Contains(trimmed, "\n") {
		t.Fatalf("stdout contained more than one line: %q", stdout)
	}
	if !filepath.IsAbs(trimmed) {
		t.Fatalf("stdout path is not absolute: %q", trimmed)
	}

	return trimmed
}

// worktreeInfo is a minimal, test-local parse of one "git worktree list --porcelain" record.
// Bare is set for the bare repository's own entry, which this layout's porcelain output
// includes alongside the linked main/slot-* worktrees; it is never a slot or "main" itself.
type worktreeInfo struct {
	Path     string
	Branch   string
	Detached bool
	Bare     bool
}

// listWorktrees parses git's own porcelain output directly, independent of anything wt
// reports, so tests can check wt's view of the world against ground truth.
func listWorktrees(t *testing.T, root string) []worktreeInfo {
	t.Helper()

	out := runGit(t, root, "worktree", "list", "--porcelain")

	var result []worktreeInfo
	var current worktreeInfo

	flush := func() {
		if current.Path != "" {
			result = append(result, current)
		}
		current = worktreeInfo{}
	}

	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			current.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			current.Detached = true
		case line == "bare":
			current.Bare = true
		}
	}
	flush()

	return result
}

// namedWorktrees filters out the bare repository's own porcelain entry, leaving only "main"
// and the slot-* worktrees that wt actually manages.
func namedWorktrees(wts []worktreeInfo) []worktreeInfo {
	var named []worktreeInfo
	for _, w := range wts {
		if !w.Bare {
			named = append(named, w)
		}
	}
	return named
}

// wtSlotEntry and wtStateFile mirror the on-disk shape of .bare/wt.json from agent-plan.md
// §4: {"version":1,"slots":{"slot-N":{"last_used":"<RFC3339>"}}}.
type wtSlotEntry struct {
	LastUsed time.Time `json:"last_used"`
}

type wtStateFile struct {
	Version int                    `json:"version"`
	Slots   map[string]wtSlotEntry `json:"slots"`
}

func readWTState(t *testing.T, root string) wtStateFile {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, ".bare", "wt.json"))
	if err != nil {
		t.Fatalf("reading wt.json: %v", err)
	}

	var s wtStateFile
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parsing wt.json: %v", err)
	}

	return s
}

func writeWTState(t *testing.T, root string, s wtStateFile) {
	t.Helper()

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatalf("marshaling wt.json: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, ".bare", "wt.json"), data, 0o644); err != nil {
		t.Fatalf("writing wt.json: %v", err)
	}
}

// makeAllSlotsRecentExcept rewrites wt.json so every slot-1..slot-6 entry has a fresh
// timestamp except oldest, which gets a timestamp far in the past. This deterministically
// arranges LRU order for tests without sleeping between commands.
func makeAllSlotsRecentExcept(t *testing.T, root, oldest string) {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Second)
	s := readWTState(t, root)
	if s.Slots == nil {
		s.Slots = map[string]wtSlotEntry{}
	}

	for i := 1; i <= 6; i++ {
		name := fmt.Sprintf("slot-%d", i)
		if name == oldest {
			continue
		}
		s.Slots[name] = wtSlotEntry{LastUsed: now}
	}
	s.Slots[oldest] = wtSlotEntry{LastUsed: now.Add(-72 * time.Hour)}
	s.Version = 1

	writeWTState(t, root, s)
}

// newRemote builds a local bare "remote" with a seeded main branch and several feature
// branches, all reachable, so wt clone and wt go have real refs to work against.
func newRemote(t *testing.T) string {
	t.Helper()

	base := t.TempDir()
	work := filepath.Join(base, "work")
	remote := filepath.Join(base, "remote.git")

	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("creating work dir: %v", err)
	}

	runGit(t, base, "init", "--bare", "-b", "main", remote)
	runGit(t, work, "init", "-b", "main")

	readme := filepath.Join(work, "README.md")
	if err := os.WriteFile(readme, []byte("test repo\n"), 0o644); err != nil {
		t.Fatalf("writing README.md: %v", err)
	}

	runGit(t, work, "add", "README.md")
	runGit(t, work, "commit", "-m", "initial commit")

	branches := []string{"feat-a", "feat-b", "feat-c", "feat-d", "feat-e", "feat-f", "feat-g"}
	for _, b := range branches {
		runGit(t, work, "branch", b, "main")
	}

	runGit(t, work, "remote", "add", "origin", remote)

	pushArgs := append([]string{"push", "origin", "main"}, branches...)
	runGit(t, work, pushArgs...)

	return remote
}

// cloneRepo builds a fresh remote and runs the real wt clone against it, returning the new
// repo root. It is fresh per call so tests never share slot or LRU state with each other.
func cloneRepo(t *testing.T) string {
	t.Helper()

	remote := newRemote(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, "repo")

	stdout, stderr, code := runWT(t, parent, "clone", remote, dir)
	if code != 0 {
		t.Fatalf("wt clone %s %s: exit=%d stdout=%q stderr=%q", remote, dir, code, stdout, stderr)
	}

	return dir
}

// occupyAllSlots runs "wt go <branch>" for each of six branches against a freshly cloned
// repo, filling every slot from the initially all-idle pool.
func occupyAllSlots(t *testing.T, dir string, branches [6]string) {
	t.Helper()

	for _, b := range branches {
		_, stderr, code := runWT(t, dir, "go", b)
		if code != 0 {
			t.Fatalf("wt go %s while occupying slots: exit=%d stderr=%q", b, code, stderr)
		}
	}
}

func TestCloneLayout(t *testing.T) {
	dir := cloneRepo(t)

	bareDir := filepath.Join(dir, ".bare")
	if info, err := os.Stat(bareDir); err != nil || !info.IsDir() {
		t.Fatalf(".bare is not a directory: err=%v", err)
	}

	gitFile := filepath.Join(dir, ".git")
	info, err := os.Stat(gitFile)
	if err != nil {
		t.Fatalf("stat .git: %v", err)
	}
	if info.IsDir() {
		t.Fatalf(".git should be a regular file, found a directory")
	}

	contents, err := os.ReadFile(gitFile)
	if err != nil {
		t.Fatalf("reading .git file: %v", err)
	}
	if got := strings.TrimSpace(string(contents)); got != "gitdir: ./.bare" {
		t.Fatalf(".git file contents = %q, want %q", got, "gitdir: ./.bare")
	}

	mainDir := filepath.Join(dir, "main")
	if info, err := os.Stat(mainDir); err != nil || !info.IsDir() {
		t.Fatalf("main worktree missing: err=%v", err)
	}

	for i := 1; i <= 6; i++ {
		slot := filepath.Join(dir, fmt.Sprintf("slot-%d", i))
		if info, err := os.Stat(slot); err != nil || !info.IsDir() {
			t.Fatalf("%s missing: err=%v", slot, err)
		}

		head := runGit(t, slot, "rev-parse", "--abbrev-ref", "HEAD")
		if head != "HEAD" {
			t.Fatalf("%s: expected detached HEAD, got branch %q", slot, head)
		}
	}

	if out := runGit(t, mainDir, "status", "--porcelain"); out != "" {
		t.Fatalf("main status --porcelain not empty: %q", out)
	}

	runGit(t, dir, "fetch")

	s := readWTState(t, dir)
	if len(s.Slots) != 6 {
		t.Fatalf("wt.json has %d slots, want 6", len(s.Slots))
	}
	if _, ok := s.Slots["main"]; ok {
		t.Fatalf(`wt.json should not contain a "main" slot entry`)
	}
}

type goLocationCase struct {
	Name   string
	Branch string
	RelDir string
}

var goLocationCases = []goLocationCase{
	{Name: "from main worktree", Branch: "feature-from-main", RelDir: "main"},
	{Name: "from a slot", Branch: "feature-from-slot", RelDir: "slot-1"},
	{Name: "from repo root", Branch: "feature-from-root", RelDir: "."},
}

func TestGoAssignsSlotFromVariousLocations(t *testing.T) {
	for _, tc := range goLocationCases {
		t.Run(tc.Name, func(t *testing.T) {
			dir := cloneRepo(t)
			cwd := filepath.Join(dir, tc.RelDir)

			stdout, stderr, code := runWT(t, cwd, "go", tc.Branch)
			if code != 0 {
				t.Fatalf("wt go %s: exit=%d stderr=%q", tc.Branch, code, stderr)
			}

			slotPath := assertSingleLinePath(t, stdout)
			if !strings.HasPrefix(filepath.Base(slotPath), "slot-") {
				t.Fatalf("printed path %q does not look like a slot", slotPath)
			}

			info, err := os.Stat(slotPath)
			if err != nil || !info.IsDir() {
				t.Fatalf("printed path %q is not a directory: %v", slotPath, err)
			}

			found := false
			for _, w := range listWorktrees(t, dir) {
				if resolvePath(t, w.Path) == resolvePath(t, slotPath) && w.Branch == tc.Branch {
					found = true
				}
			}
			if !found {
				t.Fatalf("branch %q not checked out at %q according to git worktree list", tc.Branch, slotPath)
			}
		})
	}
}

func TestGoRoutesToExistingWorktree(t *testing.T) {
	t.Run("already checked out branch", func(t *testing.T) {
		dir := cloneRepo(t)

		stdout1, stderr1, code1 := runWT(t, dir, "go", "feat-a")
		if code1 != 0 {
			t.Fatalf("first wt go feat-a: exit=%d stderr=%q", code1, stderr1)
		}
		path1 := assertSingleLinePath(t, stdout1)

		stdout2, stderr2, code2 := runWT(t, dir, "go", "feat-a")
		if code2 != 0 {
			t.Fatalf("second wt go feat-a: exit=%d stderr=%q", code2, stderr2)
		}
		path2 := assertSingleLinePath(t, stdout2)

		if resolvePath(t, path1) != resolvePath(t, path2) {
			t.Fatalf("second wt go feat-a routed to a different path: %q vs %q", path1, path2)
		}
	})

	t.Run("main branch routes to main worktree", func(t *testing.T) {
		dir := cloneRepo(t)

		stdout, stderr, code := runWT(t, dir, "go", "main")
		if code != 0 {
			t.Fatalf("wt go main: exit=%d stderr=%q", code, stderr)
		}
		path := assertSingleLinePath(t, stdout)

		wantMain := resolvePath(t, filepath.Join(dir, "main"))
		if resolvePath(t, path) != wantMain {
			t.Fatalf("wt go main printed %q, want %q", path, wantMain)
		}
	})
}

func TestGoChecksOutRemoteTrackingBranch(t *testing.T) {
	dir := cloneRepo(t)

	stdout, stderr, code := runWT(t, dir, "go", "feat-b")
	if code != 0 {
		t.Fatalf("wt go feat-b: exit=%d stderr=%q", code, stderr)
	}
	slotPath := assertSingleLinePath(t, stdout)

	upstream := runGit(t, slotPath, "rev-parse", "--abbrev-ref", "@{u}")
	if upstream != "origin/feat-b" {
		t.Fatalf("upstream = %q, want origin/feat-b", upstream)
	}
}

// TestGoRefusesDirtySlotWithoutTTY covers the refusal branch of the overwrite prompt: with no
// controlling terminal, wt cannot open /dev/tty and must abort with exit 2 rather than
// silently discarding work. The complementary "y" confirmation path is not covered by this
// suite; it needs a real pty, which stdlib testing does not provide.
func TestGoRefusesDirtySlotWithoutTTY(t *testing.T) {
	dir := cloneRepo(t)

	occupyAllSlots(t, dir, [6]string{"feat-a", "feat-b", "feat-c", "feat-d", "feat-e", "feat-f"})

	var dirtySlot, dirtyBranch string
	for _, w := range listWorktrees(t, dir) {
		if w.Branch == "feat-f" {
			dirtySlot = w.Path
			dirtyBranch = w.Branch
		}
	}
	if dirtySlot == "" {
		t.Fatalf("could not find slot holding feat-f")
	}

	untracked := filepath.Join(dirtySlot, "untracked.txt")
	if err := os.WriteFile(untracked, []byte("scratch\n"), 0o644); err != nil {
		t.Fatalf("writing untracked file: %v", err)
	}

	readme := filepath.Join(dirtySlot, "README.md")
	original, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	dirtied := append(append([]byte{}, original...), []byte("local edit\n")...)
	if err := os.WriteFile(readme, dirtied, 0o644); err != nil {
		t.Fatalf("modifying README.md: %v", err)
	}

	makeAllSlotsRecentExcept(t, dir, filepath.Base(dirtySlot))

	_, stderr, code := runWT(t, dir, "go", "brand-new-1")
	if code != 2 {
		t.Fatalf("wt go brand-new-1 against a dirty slot: exit=%d, want 2; stderr=%q", code, stderr)
	}

	if _, err := os.Stat(untracked); err != nil {
		t.Fatalf("untracked file was removed by an aborted wt go: %v", err)
	}

	afterReadme, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("reading README.md after abort: %v", err)
	}
	if string(afterReadme) != string(dirtied) {
		t.Fatalf("README.md was changed by an aborted wt go")
	}

	currentBranch := runGit(t, dirtySlot, "rev-parse", "--abbrev-ref", "HEAD")
	if currentBranch != dirtyBranch {
		t.Fatalf("slot branch changed after an aborted wt go: got %q, want %q", currentBranch, dirtyBranch)
	}

	if out := runGit(t, dir, "branch", "--list", "brand-new-1"); out != "" {
		t.Fatalf("branch brand-new-1 should not exist after an aborted wt go, found: %q", out)
	}
}

func TestGoPicksLeastRecentlyUsedSlot(t *testing.T) {
	dir := cloneRepo(t)

	occupyAllSlots(t, dir, [6]string{"feat-a", "feat-b", "feat-c", "feat-d", "feat-e", "feat-f"})

	victim := filepath.Join(dir, "slot-4")
	makeAllSlotsRecentExcept(t, dir, "slot-4")

	stdout, stderr, code := runWT(t, dir, "go", "feat-g")
	if code != 0 {
		t.Fatalf("wt go feat-g: exit=%d stderr=%q", code, stderr)
	}

	slotPath := assertSingleLinePath(t, stdout)
	if resolvePath(t, slotPath) != resolvePath(t, victim) {
		t.Fatalf("wt go feat-g used %q, want the LRU slot %q", slotPath, victim)
	}

	branch := runGit(t, victim, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "feat-g" {
		t.Fatalf("slot-4 holds branch %q after wt go feat-g, want feat-g", branch)
	}
}

func TestGoPrefersIdleSlot(t *testing.T) {
	dir := cloneRepo(t)

	occupyAllSlots(t, dir, [6]string{"feat-a", "feat-b", "feat-c", "feat-d", "feat-e", "feat-f"})

	var idleSlot string
	for _, w := range listWorktrees(t, dir) {
		if w.Branch == "feat-a" {
			idleSlot = w.Path
		}
	}
	if idleSlot == "" {
		t.Fatalf("could not find slot holding feat-a")
	}
	idleSlotName := filepath.Base(idleSlot)

	_, stderr, code := runWT(t, dir, "release", "feat-a")
	if code != 0 {
		t.Fatalf("wt release feat-a: exit=%d stderr=%q", code, stderr)
	}

	// Give the now-idle slot the most recent timestamp and every occupied slot an older
	// one. If idle-first selection were broken, plain LRU-by-timestamp would pick an
	// occupied slot instead, because the idle slot looks like the most recently used one.
	now := time.Now().UTC().Truncate(time.Second)
	s := readWTState(t, dir)
	for name := range s.Slots {
		if name == idleSlotName {
			s.Slots[name] = wtSlotEntry{LastUsed: now}
		} else {
			s.Slots[name] = wtSlotEntry{LastUsed: now.Add(-48 * time.Hour)}
		}
	}
	writeWTState(t, dir, s)

	stdout, stderr, code := runWT(t, dir, "go", "brand-new-idle-test")
	if code != 0 {
		t.Fatalf("wt go brand-new-idle-test: exit=%d stderr=%q", code, stderr)
	}
	slotPath := assertSingleLinePath(t, stdout)

	if resolvePath(t, slotPath) != resolvePath(t, idleSlot) {
		t.Fatalf("wt go picked %q, want the idle slot %q", slotPath, idleSlot)
	}
}

func TestRelease(t *testing.T) {
	t.Run("release by branch name detaches the slot", func(t *testing.T) {
		dir := cloneRepo(t)

		stdout, stderr, code := runWT(t, dir, "go", "feat-a")
		if code != 0 {
			t.Fatalf("wt go feat-a: exit=%d stderr=%q", code, stderr)
		}
		slotPath := assertSingleLinePath(t, stdout)

		_, relStderr, relCode := runWT(t, dir, "release", "feat-a")
		if relCode != 0 {
			t.Fatalf("wt release feat-a: exit=%d stderr=%q", relCode, relStderr)
		}

		head := runGit(t, slotPath, "rev-parse", "--abbrev-ref", "HEAD")
		if head != "HEAD" {
			t.Fatalf("slot branch after release = %q, want detached HEAD", head)
		}

		defaultCommit := runGit(t, filepath.Join(dir, "main"), "rev-parse", "HEAD")
		slotCommit := runGit(t, slotPath, "rev-parse", "HEAD")
		if slotCommit != defaultCommit {
			t.Fatalf("released slot HEAD = %q, want default branch HEAD %q", slotCommit, defaultCommit)
		}
	})

	t.Run("release with no argument releases the current slot", func(t *testing.T) {
		dir := cloneRepo(t)

		stdout, stderr, code := runWT(t, dir, "go", "feat-b")
		if code != 0 {
			t.Fatalf("wt go feat-b: exit=%d stderr=%q", code, stderr)
		}
		slotPath := assertSingleLinePath(t, stdout)

		_, relStderr, relCode := runWT(t, slotPath, "release")
		if relCode != 0 {
			t.Fatalf("wt release (no arg): exit=%d stderr=%q", relCode, relStderr)
		}

		head := runGit(t, slotPath, "rev-parse", "--abbrev-ref", "HEAD")
		if head != "HEAD" {
			t.Fatalf("slot branch after release = %q, want detached HEAD", head)
		}
	})

	t.Run("releasing main is refused", func(t *testing.T) {
		dir := cloneRepo(t)

		_, stderr, code := runWT(t, dir, "release", "main")
		if code != 1 {
			t.Fatalf("wt release main: exit=%d, want 1; stderr=%q", code, stderr)
		}
		if stderr == "" {
			t.Fatalf("wt release main: expected an error message on stderr")
		}
	})
}

func TestList(t *testing.T) {
	dir := cloneRepo(t)

	stdout, stderr, code := runWT(t, dir, "go", "feat-a")
	if code != 0 {
		t.Fatalf("wt go feat-a: exit=%d stderr=%q", code, stderr)
	}
	slotPath := assertSingleLinePath(t, stdout)
	slotName := filepath.Base(slotPath)

	listOut, listStderr, listCode := runWT(t, dir, "list")
	if listCode != 0 {
		t.Fatalf("wt list: exit=%d stderr=%q", listCode, listStderr)
	}

	if !strings.Contains(listOut, "main") {
		t.Fatalf("wt list output missing a main row:\n%s", listOut)
	}
	if !strings.Contains(listOut, "feat-a") {
		t.Fatalf("wt list output missing feat-a:\n%s", listOut)
	}
	if !strings.Contains(listOut, "(idle)") {
		t.Fatalf("wt list output missing an idle marker:\n%s", listOut)
	}

	occupiedRowFound := false
	for _, line := range strings.Split(listOut, "\n") {
		if strings.Contains(line, slotName) && strings.Contains(line, "feat-a") {
			occupiedRowFound = true
		}
	}
	if !occupiedRowFound {
		t.Fatalf("wt list has no row pairing %s with feat-a:\n%s", slotName, listOut)
	}

	named := namedWorktrees(listWorktrees(t, dir))
	if len(named) != 7 {
		t.Fatalf("git worktree list reports %d non-bare worktrees, want 7 (main + 6 slots): %+v", len(named), named)
	}
	for _, w := range named {
		name := filepath.Base(w.Path)
		if !strings.Contains(listOut, name) {
			t.Fatalf("wt list output missing worktree %q present in git worktree list:\n%s", name, listOut)
		}
	}
}

// columnSplitRe splits a wt list row into columns. Tabwriter pads every column to at least
// two spaces past its content, so a run of two or more spaces reliably marks a column
// boundary even though a STATE value ("dirty, unpushed") can contain a single embedded space.
var columnSplitRe = regexp.MustCompile(`\s{2,}`)

// stateColumn returns the STATE column of the wt list row whose WORKTREE column is name.
func stateColumn(t *testing.T, listOut, name string) string {
	t.Helper()

	for _, line := range strings.Split(listOut, "\n") {
		fields := columnSplitRe.Split(strings.TrimRight(line, " "), -1)
		if len(fields) > 0 && fields[0] == name {
			if len(fields) < 3 {
				t.Fatalf("wt list row for %q has too few columns: %q", name, line)
			}
			return fields[2]
		}
	}

	t.Fatalf("wt list output has no row for %q:\n%s", name, listOut)
	return ""
}

// stateColumnCase pairs a slot name, resolved at runtime since wt go's LRU choice of slot
// isn't fixed, with the STATE column value that slot's row must render.
type stateColumnCase struct {
	Name string
	Slot string
	Want string
}

// TestListStateColumn drives wt list against slots in each of the states the single-subprocess
// QuickStatus must still distinguish: clean, dirty, unpushed via no upstream, and unpushed via
// commits ahead of a configured upstream.
func TestListStateColumn(t *testing.T) {
	dir := cloneRepo(t)

	stdout, stderr, code := runWT(t, dir, "go", "feat-a")
	if code != 0 {
		t.Fatalf("wt go feat-a: exit=%d stderr=%q", code, stderr)
	}
	cleanSlot := filepath.Base(assertSingleLinePath(t, stdout))

	stdout, stderr, code = runWT(t, dir, "go", "feat-b")
	if code != 0 {
		t.Fatalf("wt go feat-b: exit=%d stderr=%q", code, stderr)
	}
	dirtySlotPath := assertSingleLinePath(t, stdout)
	dirtySlot := filepath.Base(dirtySlotPath)
	if err := os.WriteFile(filepath.Join(dirtySlotPath, "scratch.txt"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatalf("writing scratch file: %v", err)
	}

	stdout, stderr, code = runWT(t, dir, "go", "brand-new-local")
	if code != 0 {
		t.Fatalf("wt go brand-new-local: exit=%d stderr=%q", code, stderr)
	}
	localOnlySlot := filepath.Base(assertSingleLinePath(t, stdout))

	stdout, stderr, code = runWT(t, dir, "go", "feat-c")
	if code != 0 {
		t.Fatalf("wt go feat-c: exit=%d stderr=%q", code, stderr)
	}
	aheadSlotPath := assertSingleLinePath(t, stdout)
	aheadSlot := filepath.Base(aheadSlotPath)
	if err := os.WriteFile(filepath.Join(aheadSlotPath, "local-commit.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatalf("writing local-commit file: %v", err)
	}
	runGit(t, aheadSlotPath, "add", "local-commit.txt")
	runGit(t, aheadSlotPath, "commit", "-m", "local only commit")

	listOut, listStderr, listCode := runWT(t, dir, "list")
	if listCode != 0 {
		t.Fatalf("wt list: exit=%d stderr=%q", listCode, listStderr)
	}

	cases := []stateColumnCase{
		{Name: "clean slot on an in-sync remote-tracked branch", Slot: cleanSlot, Want: "clean"},
		{Name: "dirty slot on an in-sync remote-tracked branch", Slot: dirtySlot, Want: "dirty*"},
		{Name: "slot on a local-only branch", Slot: localOnlySlot, Want: "unpushed*"},
		{Name: "slot ahead of its remote-tracked upstream", Slot: aheadSlot, Want: "unpushed*"},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			got := stateColumn(t, listOut, tc.Slot)
			if got != tc.Want {
				t.Fatalf("STATE column for %s = %q, want %q\nfull output:\n%s", tc.Slot, got, tc.Want, listOut)
			}
		})
	}
}

func TestOutOfBandRemoval(t *testing.T) {
	dir := cloneRepo(t)

	runGit(t, dir, "worktree", "remove", "--force", "slot-3")

	listOut, listStderr, listCode := runWT(t, dir, "list")
	if listCode != 0 {
		t.Fatalf("wt list after out-of-band removal: exit=%d stderr=%q", listCode, listStderr)
	}
	if strings.Contains(listOut, "slot-3") {
		t.Fatalf("wt list still lists removed slot-3:\n%s", listOut)
	}

	for i := range 5 {
		branch := fmt.Sprintf("brand-new-oob-%d", i)
		_, stderr, code := runWT(t, dir, "go", branch)
		if code != 0 {
			t.Fatalf("wt go %s: exit=%d stderr=%q", branch, code, stderr)
		}
	}

	for _, w := range listWorktrees(t, dir) {
		if filepath.Base(w.Path) == "slot-3" {
			t.Fatalf("wt go recreated or reused the removed slot-3")
		}
	}
}

func TestMissingStateFile(t *testing.T) {
	dir := cloneRepo(t)

	stateFile := filepath.Join(dir, ".bare", "wt.json")
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing wt.json: %v", err)
	}

	listOut, listStderr, listCode := runWT(t, dir, "list")
	if listCode != 0 {
		t.Fatalf("wt list with missing wt.json: exit=%d stderr=%q", listCode, listStderr)
	}
	if !strings.Contains(listOut, "main") {
		t.Fatalf("wt list output missing a main row:\n%s", listOut)
	}

	stdout, goStderr, goCode := runWT(t, dir, "go", "feat-c")
	if goCode != 0 {
		t.Fatalf("wt go feat-c with missing wt.json: exit=%d stderr=%q", goCode, goStderr)
	}
	slotPath := assertSingleLinePath(t, stdout)

	branch := runGit(t, slotPath, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "feat-c" {
		t.Fatalf("slot branch = %q after wt go feat-c, want feat-c", branch)
	}

	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("wt.json was not recreated after wt go: %v", err)
	}
}

func TestMiscCommands(t *testing.T) {
	t.Run("wt shell-init emits shell wrapper and completion", func(t *testing.T) {
		dir := t.TempDir()

		stdout, stderr, code := runWT(t, dir, "shell-init")
		if code != 0 {
			t.Fatalf("wt shell-init: exit=%d stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "wt()") {
			t.Fatalf("wt shell-init output missing the wt() wrapper function:\n%s", stdout)
		}
		if !strings.Contains(stdout, "for-each-ref") {
			t.Fatalf("wt shell-init output missing the for-each-ref completion script:\n%s", stdout)
		}
	})

	t.Run("wt version prints a non-empty version string", func(t *testing.T) {
		dir := t.TempDir()

		stdout, stderr, code := runWT(t, dir, "version")
		if code != 0 {
			t.Fatalf("wt version: exit=%d stderr=%q", code, stderr)
		}
		if strings.TrimSpace(stdout) == "" {
			t.Fatalf("wt version produced no stdout output")
		}
	})

	t.Run("unknown subcommand exits 1", func(t *testing.T) {
		dir := t.TempDir()

		_, stderr, code := runWT(t, dir, "bogus-subcommand")
		if code != 1 {
			t.Fatalf("wt bogus-subcommand: exit=%d, want 1; stderr=%q", code, stderr)
		}
		if stderr == "" {
			t.Fatalf("wt bogus-subcommand: expected an error message on stderr")
		}
	})

	t.Run("wt go outside a wt repo exits 1", func(t *testing.T) {
		outside := t.TempDir()

		_, stderr, code := runWT(t, outside, "go", "anything")
		if code != 1 {
			t.Fatalf("wt go outside a wt repo: exit=%d, want 1; stderr=%q", code, stderr)
		}
		if !strings.Contains(stderr, "not a wt-managed repository") {
			t.Fatalf("wt go outside a wt repo: stderr = %q, want it to mention \"not a wt-managed repository\"", stderr)
		}
	})
}

// dirtySlotHolding fills every slot, dirties the one holding branch (an untracked file
// plus a tracked-file edit), makes it the LRU victim, and returns its path.
func dirtySlotHolding(t *testing.T, dir, branch string) string {
	t.Helper()

	occupyAllSlots(t, dir, [6]string{"feat-a", "feat-b", "feat-c", "feat-d", "feat-e", branch})

	var dirtySlot string
	for _, w := range listWorktrees(t, dir) {
		if w.Branch == branch {
			dirtySlot = w.Path
		}
	}
	if dirtySlot == "" {
		t.Fatalf("could not find slot holding %s", branch)
	}

	if err := os.WriteFile(filepath.Join(dirtySlot, "untracked.txt"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatalf("writing untracked file: %v", err)
	}
	readme := filepath.Join(dirtySlot, "README.md")
	if err := os.WriteFile(readme, []byte("local edit\n"), 0o644); err != nil {
		t.Fatalf("modifying README.md: %v", err)
	}

	makeAllSlotsRecentExcept(t, dir, filepath.Base(dirtySlot))

	return dirtySlot
}

// TestGoYesOverwritesDirtySlot covers the --yes path of the overwrite prompt: the flag
// stands in for the interactive "y" that this suite cannot type (no pty), so the overwrite
// proceeds with no controlling terminal instead of aborting with exit 2.
func TestGoYesOverwritesDirtySlot(t *testing.T) {
	dir := cloneRepo(t)
	dirtySlot := dirtySlotHolding(t, dir, "feat-f")

	stdout, stderr, code := runWT(t, dir, "go", "--yes", "brand-new-1")
	if code != 0 {
		t.Fatalf("wt go --yes brand-new-1 against a dirty slot: exit=%d stderr=%q", code, stderr)
	}
	slotPath := assertSingleLinePath(t, stdout)
	if resolvePath(t, slotPath) != resolvePath(t, dirtySlot) {
		t.Fatalf("wt go --yes used %q, want the dirty LRU slot %q", slotPath, dirtySlot)
	}

	if branch := runGit(t, dirtySlot, "rev-parse", "--abbrev-ref", "HEAD"); branch != "brand-new-1" {
		t.Fatalf("slot branch after wt go --yes = %q, want brand-new-1", branch)
	}
	if _, err := os.Stat(filepath.Join(dirtySlot, "untracked.txt")); !os.IsNotExist(err) {
		t.Fatalf("untracked file should have been cleaned by wt go --yes, stat err = %v", err)
	}
}

// TestReleaseDirtySlot covers both sides of the release safety prompt on a dirty slot:
// without a controlling terminal it must abort with exit 2 and leave the slot untouched;
// with --yes it must proceed, discard the dirty state, and return the slot to idle.
func TestReleaseDirtySlot(t *testing.T) {
	t.Run("refuses without a tty", func(t *testing.T) {
		dir := cloneRepo(t)
		dirtySlot := dirtySlotHolding(t, dir, "feat-f")

		_, stderr, code := runWT(t, dir, "release", "feat-f")
		if code != 2 {
			t.Fatalf("wt release feat-f against a dirty slot: exit=%d, want 2; stderr=%q", code, stderr)
		}
		if branch := runGit(t, dirtySlot, "rev-parse", "--abbrev-ref", "HEAD"); branch != "feat-f" {
			t.Fatalf("slot branch changed after an aborted wt release: got %q, want feat-f", branch)
		}
		if _, err := os.Stat(filepath.Join(dirtySlot, "untracked.txt")); err != nil {
			t.Fatalf("untracked file was removed by an aborted wt release: %v", err)
		}
	})

	t.Run("--yes releases without a tty", func(t *testing.T) {
		dir := cloneRepo(t)
		dirtySlot := dirtySlotHolding(t, dir, "feat-f")

		_, stderr, code := runWT(t, dir, "release", "--yes", "feat-f")
		if code != 0 {
			t.Fatalf("wt release --yes feat-f: exit=%d stderr=%q", code, stderr)
		}
		if head := runGit(t, dirtySlot, "rev-parse", "--abbrev-ref", "HEAD"); head != "HEAD" {
			t.Fatalf("slot after wt release --yes = %q, want detached HEAD", head)
		}
		if _, err := os.Stat(filepath.Join(dirtySlot, "untracked.txt")); !os.IsNotExist(err) {
			t.Fatalf("untracked file should have been cleaned by wt release --yes, stat err = %v", err)
		}
	})
}

// TestReleaseYesDeleteBranchUnpushed covers the second confirm site in release: deleting a
// branch with unpushed commits. --yes must stand in for the prompt there too.
func TestReleaseYesDeleteBranchUnpushed(t *testing.T) {
	dir := cloneRepo(t)

	stdout, stderr, code := runWT(t, dir, "go", "feat-a")
	if code != 0 {
		t.Fatalf("wt go feat-a: exit=%d stderr=%q", code, stderr)
	}
	slotPath := assertSingleLinePath(t, stdout)

	if err := os.WriteFile(filepath.Join(slotPath, "new.txt"), []byte("unpushed\n"), 0o644); err != nil {
		t.Fatalf("writing new file: %v", err)
	}
	runGit(t, slotPath, "add", "new.txt")
	runGit(t, slotPath, "commit", "-m", "unpushed work")

	_, stderr, code = runWT(t, dir, "release", "--delete-branch", "feat-a")
	if code != 2 {
		t.Fatalf("wt release --delete-branch with unpushed commits and no tty: exit=%d, want 2; stderr=%q", code, stderr)
	}
	if out := runGit(t, dir, "branch", "--list", "feat-a"); out == "" {
		t.Fatalf("branch feat-a was deleted by an aborted wt release")
	}

	_, stderr, code = runWT(t, dir, "release", "--yes", "--delete-branch", "feat-a")
	if code != 0 {
		t.Fatalf("wt release --yes --delete-branch feat-a: exit=%d stderr=%q", code, stderr)
	}
	if out := runGit(t, dir, "branch", "--list", "feat-a"); out != "" {
		t.Fatalf("branch feat-a should have been deleted, found: %q", out)
	}
	if head := runGit(t, slotPath, "rev-parse", "--abbrev-ref", "HEAD"); head != "HEAD" {
		t.Fatalf("slot after release = %q, want detached HEAD", head)
	}
}

// runWTPwd is runWT with the child's $PWD set to dir, reproducing how shells behave in a
// directory reached through a symlink: os.Getwd honors $PWD when it points at the same
// inode as ".", so wt sees the symlinked spelling while git reports canonical paths.
func runWTPwd(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	cmd := exec.Command(wtBinary, args...)
	cmd.Dir = dir
	cmd.Env = append(testEnv(), "PWD="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode()
		}
		t.Fatalf("running wt %v: %v", args, err)
	}

	return outBuf.String(), errBuf.String(), 0
}

// TestSymlinkedCWD runs wt from a repo path spelled through a symlink, the way a shell
// user reaches a repo via a symlinked parent directory (~/code -> /mnt/…). Every "is this
// worktree a child of root?" comparison must survive $PWD and git disagreeing on spelling:
// slot selection in wt go, the root filter in wt list, release by slot name, and the
// current-slot lookup of a bare wt release.
func TestSymlinkedCWD(t *testing.T) {
	dir := cloneRepo(t)

	link := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	stdout, stderr, code := runWTPwd(t, link, "go", "feat-a")
	if code != 0 {
		t.Fatalf("wt go feat-a from symlinked cwd: exit=%d stderr=%q", code, stderr)
	}
	slotPath := assertSingleLinePath(t, stdout)
	// The printed path must be the canonical spelling — the one git reports — not the
	// symlinked spelling of the cwd, so the shell wrapper cds to the resolved location.
	if resolved := resolvePath(t, slotPath); slotPath != resolved {
		t.Fatalf("wt go from symlinked cwd printed %q, want the canonical path %q", slotPath, resolved)
	}
	slotName := filepath.Base(slotPath)

	listOut, listStderr, listCode := runWTPwd(t, link, "list")
	if listCode != 0 {
		t.Fatalf("wt list from symlinked cwd: exit=%d stderr=%q", listCode, listStderr)
	}
	if !strings.Contains(listOut, "feat-a") {
		t.Fatalf("wt list from symlinked cwd is missing the occupied branch:\n%s", listOut)
	}
	if !strings.Contains(listOut, slotName) {
		t.Fatalf("wt list from symlinked cwd is missing the %s row:\n%s", slotName, listOut)
	}

	_, relStderr, relCode := runWTPwd(t, link, "release", slotName)
	if relCode != 0 {
		t.Fatalf("wt release %s from symlinked cwd: exit=%d stderr=%q", slotName, relCode, relStderr)
	}
	if head := runGit(t, slotPath, "rev-parse", "--abbrev-ref", "HEAD"); head != "HEAD" {
		t.Fatalf("slot after release from symlinked cwd = %q, want detached HEAD", head)
	}

	stdout, stderr, code = runWTPwd(t, link, "go", "feat-b")
	if code != 0 {
		t.Fatalf("wt go feat-b from symlinked cwd: exit=%d stderr=%q", code, stderr)
	}
	slotName = filepath.Base(assertSingleLinePath(t, stdout))

	// A bare `wt release` resolves the slot from the cwd — here spelled through the link.
	linkedSlot := filepath.Join(link, slotName)
	_, relStderr, relCode = runWTPwd(t, linkedSlot, "release")
	if relCode != 0 {
		t.Fatalf("wt release (no arg) from symlinked slot cwd: exit=%d stderr=%q", relCode, relStderr)
	}
}
