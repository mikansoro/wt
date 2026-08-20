package integration

// Black-box integration tests for `wt adopt`. Fixtures build a hand-rolled bare+worktrees
// layout the way `git clone --bare` actually produces one — branches copied as plain local
// heads, no remote-tracking refspec, no main/slot-N convention — and drive the compiled
// binary against it, exactly like integration_test.go does for the rest of the CLI.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// adoptFixture is a bare clone under an arbitrary name (not .bare) sitting inside base,
// the directory that `wt adopt` is meant to convert into a repo root.
type adoptFixture struct {
	base    string
	bareDir string
	remote  string
}

// newAdoptFixture bare-clones a fresh remote into base/<bareName>. `git clone --bare` does
// not configure the remote-tracking refspec on its own, so this is exactly the layout
// docs/adopting-existing-repos.md and `wt adopt` both exist to fix.
func newAdoptFixture(t *testing.T, bareName string) adoptFixture {
	t.Helper()

	remote := newRemote(t)
	base := t.TempDir()
	bareDir := filepath.Join(base, bareName)

	runGit(t, base, "clone", "--bare", remote, bareDir)

	return adoptFixture{base: base, bareDir: bareDir, remote: remote}
}

// addWorktree checks out branch into base/name using an absolute path, mimicking a
// worktree someone created by hand alongside the bare repo.
func (f adoptFixture) addWorktree(t *testing.T, name, branch string) string {
	t.Helper()

	path := filepath.Join(f.base, name)
	runGit(t, f.bareDir, "worktree", "add", path, branch)

	return path
}

func requireDirExists(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %s to be a directory", path)
	}
}

func requireNotExists(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected %s not to exist", path)
	}
}

func TestAdoptShapeBConversion(t *testing.T) {
	t.Run("default branch checked out elsewhere skips main", func(t *testing.T) {
		f := newAdoptFixture(t, "repo.git")
		trunk := f.addWorktree(t, "trunk", "main")
		side := f.addWorktree(t, "sidework", "feat-a")

		_, stderr, code := runWT(t, f.base, "adopt")
		if code != 0 {
			t.Fatalf("wt adopt: exit=%d stderr=%q", code, stderr)
		}

		requireDirExists(t, filepath.Join(f.base, ".bare"))
		requireNotExists(t, f.bareDir)

		gitFile := filepath.Join(f.base, ".git")
		contents, err := os.ReadFile(gitFile)
		if err != nil {
			t.Fatalf("reading .git pointer file: %v", err)
		}
		if got := strings.TrimSpace(string(contents)); got != "gitdir: ./.bare" {
			t.Fatalf(".git pointer file = %q, want %q", got, "gitdir: ./.bare")
		}

		for n := 1; n <= 6; n++ {
			slot := filepath.Join(f.base, fmt.Sprintf("slot-%d", n))
			requireDirExists(t, slot)

			head := runGit(t, slot, "rev-parse", "--abbrev-ref", "HEAD")
			if head != "HEAD" {
				t.Fatalf("%s: want detached HEAD, got branch %q", slot, head)
			}
		}

		s := readWTState(t, f.base)
		if len(s.Slots) != 6 {
			t.Fatalf("wt.json has %d slots, want 6", len(s.Slots))
		}

		if branch := runGit(t, trunk, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
			t.Fatalf("trunk branch = %q after adopt, want main", branch)
		}
		if out := runGit(t, trunk, "status", "--porcelain"); out != "" {
			t.Fatalf("trunk status --porcelain not empty after adopt: %q", out)
		}
		if branch := runGit(t, side, "rev-parse", "--abbrev-ref", "HEAD"); branch != "feat-a" {
			t.Fatalf("sidework branch = %q after adopt, want feat-a", branch)
		}
		if out := runGit(t, side, "status", "--porcelain"); out != "" {
			t.Fatalf("sidework status --porcelain not empty after adopt: %q", out)
		}

		requireNotExists(t, filepath.Join(f.base, "main"))

		if !strings.Contains(stderr, "already checked out at") {
			t.Fatalf("stderr missing the main-elsewhere warning:\n%s", stderr)
		}
	})

	t.Run("default branch checked out nowhere creates main with upstream", func(t *testing.T) {
		f := newAdoptFixture(t, "repo.git")
		f.addWorktree(t, "sidework-a", "feat-a")
		f.addWorktree(t, "sidework-b", "feat-b")

		_, stderr, code := runWT(t, f.base, "adopt")
		if code != 0 {
			t.Fatalf("wt adopt: exit=%d stderr=%q", code, stderr)
		}

		mainDir := filepath.Join(f.base, "main")
		requireDirExists(t, mainDir)

		if branch := runGit(t, mainDir, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
			t.Fatalf("main branch = %q, want main", branch)
		}
		if upstream := runGit(t, mainDir, "rev-parse", "--abbrev-ref", "@{u}"); upstream != "origin/main" {
			t.Fatalf("main upstream = %q, want origin/main", upstream)
		}

		// End-to-end sanity: the adopted repo should behave like any other wt repo now.
		goStdout, goStderr, goCode := runWT(t, f.base, "go", "feat-c")
		if goCode != 0 {
			t.Fatalf("wt go feat-c after adopt: exit=%d stderr=%q", goCode, goStderr)
		}
		slotPath := assertSingleLinePath(t, goStdout)
		if branch := runGit(t, slotPath, "rev-parse", "--abbrev-ref", "HEAD"); branch != "feat-c" {
			t.Fatalf("slot branch = %q after wt go feat-c, want feat-c", branch)
		}

		listOut, listStderr, listCode := runWT(t, f.base, "list")
		if listCode != 0 {
			t.Fatalf("wt list after adopt: exit=%d stderr=%q", listCode, listStderr)
		}
		if !strings.Contains(listOut, "feat-c") {
			t.Fatalf("wt list missing feat-c after adopt:\n%s", listOut)
		}
	})
}

func TestAdoptDryRun(t *testing.T) {
	f := newAdoptFixture(t, "repo.git")
	f.addWorktree(t, "sidework-a", "feat-a")
	f.addWorktree(t, "sidework-b", "feat-b")

	_, stderr, code := runWT(t, f.base, "adopt", "--dry-run")
	if code != 0 {
		t.Fatalf("wt adopt --dry-run: exit=%d stderr=%q", code, stderr)
	}

	for _, want := range []string{
		"would: rename repo.git to .bare",
		"would: write .git pointer file",
		"would: fix origin fetch refspec",
		"would: create main worktree on main",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("dry-run stderr missing %q:\n%s", want, stderr)
		}
	}

	slotCount := strings.Count(stderr, "would: create slot-")
	if slotCount != 6 {
		t.Fatalf("dry-run stderr has %d 'would: create slot-' lines, want 6:\n%s", slotCount, stderr)
	}

	requireNotExists(t, filepath.Join(f.base, ".bare"))
	requireNotExists(t, filepath.Join(f.base, ".git"))
	requireDirExists(t, f.bareDir)
	requireNotExists(t, filepath.Join(f.base, "main"))
	for n := 1; n <= 6; n++ {
		requireNotExists(t, filepath.Join(f.base, fmt.Sprintf("slot-%d", n)))
	}
}

// TestAdoptDryRunAlignsHead covers the HEAD-alignment step's dry-run reporting. The bare
// repo's own HEAD is pointed away from the default branch while its remote-tracking HEAD
// is left correct, so adopt has something concrete to plan a fix for.
func TestAdoptDryRunAlignsHead(t *testing.T) {
	f := newAdoptFixture(t, "repo.git")
	f.addWorktree(t, "sidework", "feat-a")

	// set-head --auto needs refs/remotes/origin/<branch> to already exist locally, so the
	// refspec fix and a fetch have to happen before it, same as clone.go's own sequence.
	runGit(t, f.bareDir, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	runGit(t, f.bareDir, "fetch", "origin")
	runGit(t, f.bareDir, "remote", "set-head", "origin", "--auto")
	runGit(t, f.bareDir, "symbolic-ref", "HEAD", "refs/heads/feat-a")

	_, stderr, code := runWT(t, f.base, "adopt", "--dry-run")
	if code != 0 {
		t.Fatalf("wt adopt --dry-run: exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "would: align repository HEAD to main") {
		t.Fatalf("dry-run stderr missing the HEAD-alignment step:\n%s", stderr)
	}

	if _, err := os.Stat(filepath.Join(f.base, ".bare")); err == nil {
		t.Fatalf("dry-run should not have renamed the bare repo")
	}
}

func TestAdoptIdempotent(t *testing.T) {
	f := newAdoptFixture(t, "repo.git")
	f.addWorktree(t, "sidework", "feat-a")

	_, stderr1, code1 := runWT(t, f.base, "adopt")
	if code1 != 0 {
		t.Fatalf("first wt adopt: exit=%d stderr=%q", code1, stderr1)
	}

	stateBefore, err := os.ReadFile(filepath.Join(f.base, ".bare", "wt.json"))
	if err != nil {
		t.Fatalf("reading wt.json after first adopt: %v", err)
	}
	worktreesBefore := runGit(t, f.base, "worktree", "list", "--porcelain")

	_, stderr2, code2 := runWT(t, f.base, "adopt")
	if code2 != 0 {
		t.Fatalf("second wt adopt: exit=%d stderr=%q", code2, stderr2)
	}
	if !strings.Contains(stderr2, "nothing to do") {
		t.Fatalf("second wt adopt did not report nothing to do:\n%s", stderr2)
	}

	stateAfter, err := os.ReadFile(filepath.Join(f.base, ".bare", "wt.json"))
	if err != nil {
		t.Fatalf("reading wt.json after second adopt: %v", err)
	}
	if string(stateBefore) != string(stateAfter) {
		t.Fatalf("wt.json changed on the idempotent second run:\nbefore: %s\nafter:  %s", stateBefore, stateAfter)
	}

	worktreesAfter := runGit(t, f.base, "worktree", "list", "--porcelain")
	if worktreesBefore != worktreesAfter {
		t.Fatalf("worktree list changed on the idempotent second run:\nbefore: %s\nafter:  %s", worktreesBefore, worktreesAfter)
	}
}

func TestAdoptShapeAPartial(t *testing.T) {
	remote := newRemote(t)
	base := t.TempDir()
	bareDir := filepath.Join(base, ".bare")

	runGit(t, base, "clone", "--bare", remote, bareDir)
	if err := os.WriteFile(filepath.Join(base, ".git"), []byte("gitdir: ./.bare\n"), 0o644); err != nil {
		t.Fatalf("writing .git pointer file: %v", err)
	}

	runGit(t, base, "worktree", "add", "--detach", "slot-1", "main")
	runGit(t, base, "worktree", "add", "--detach", "slot-2", "main")

	oldTimestamp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	writeWTState(t, base, wtStateFile{
		Version: 1,
		Slots:   map[string]wtSlotEntry{"slot-1": {LastUsed: oldTimestamp}},
	})

	_, stderr, code := runWT(t, base, "adopt")
	if code != 0 {
		t.Fatalf("wt adopt: exit=%d stderr=%q", code, stderr)
	}

	s := readWTState(t, base)
	if len(s.Slots) != 6 {
		t.Fatalf("wt.json has %d slots after partial adopt, want 6", len(s.Slots))
	}
	if !s.Slots["slot-1"].LastUsed.Equal(oldTimestamp) {
		t.Fatalf("slot-1's pre-seeded timestamp was overwritten: got %v, want %v", s.Slots["slot-1"].LastUsed, oldTimestamp)
	}
	if _, ok := s.Slots["slot-2"]; !ok {
		t.Fatalf("slot-2 (pre-existing, no prior entry) did not get a new wt.json entry")
	}
	for n := 3; n <= 6; n++ {
		name := fmt.Sprintf("slot-%d", n)
		requireDirExists(t, filepath.Join(base, name))
		if _, ok := s.Slots[name]; !ok {
			t.Fatalf("%s missing its wt.json entry", name)
		}
	}

	if !strings.Contains(stderr, "recorded LRU entry for slot-2") {
		t.Fatalf("stderr missing the slot-2 LRU-entry line:\n%s", stderr)
	}
	if !strings.Contains(stderr, "created slot-3") {
		t.Fatalf("stderr missing slot-3 creation:\n%s", stderr)
	}
}

func TestAdoptNoRemote(t *testing.T) {
	base := t.TempDir()
	bareDir := filepath.Join(base, "repo.git")
	runGit(t, base, "init", "--bare", "-b", "main", bareDir)

	work := t.TempDir()
	runGit(t, work, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("test repo\n"), 0o644); err != nil {
		t.Fatalf("writing README.md: %v", err)
	}
	runGit(t, work, "add", "README.md")
	runGit(t, work, "commit", "-m", "initial commit")
	runGit(t, work, "push", bareDir, "main")
	runGit(t, work, "branch", "feat-a")
	runGit(t, work, "push", bareDir, "feat-a")

	runGit(t, bareDir, "worktree", "add", filepath.Join(base, "feature-x"), "feat-a")

	_, stderr, code := runWT(t, base, "adopt")
	if code != 0 {
		t.Fatalf("wt adopt on a no-remote repo: exit=%d stderr=%q", code, stderr)
	}

	for n := 1; n <= 6; n++ {
		requireDirExists(t, filepath.Join(base, fmt.Sprintf("slot-%d", n)))
	}

	if remotes := runGit(t, base, "remote"); remotes != "" {
		t.Fatalf("adopt configured a remote on a repo that had none: %q", remotes)
	}
}

func TestAdoptRemoteFlag(t *testing.T) {
	f := newAdoptFixture(t, "repo.git")
	runGit(t, f.bareDir, "remote", "rename", "origin", "upstream")

	_, stderr, code := runWT(t, f.base, "adopt")
	if code != 1 {
		t.Fatalf("wt adopt with a non-origin remote and no --remote flag: exit=%d, want 1; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "upstream") {
		t.Fatalf("stderr does not mention the actual remote name 'upstream':\n%s", stderr)
	}
	if !strings.Contains(stderr, "--remote") {
		t.Fatalf("stderr does not suggest --remote:\n%s", stderr)
	}

	requireNotExists(t, filepath.Join(f.base, ".bare"))
	requireDirExists(t, f.bareDir)

	_, stderr2, code2 := runWT(t, f.base, "adopt", "--remote", "upstream")
	if code2 != 0 {
		t.Fatalf("wt adopt --remote upstream: exit=%d stderr=%q", code2, stderr2)
	}

	wantFetch := "+refs/heads/*:refs/remotes/upstream/*"
	if got := runGit(t, f.base, "config", "--get", "remote.upstream.fetch"); got != wantFetch {
		t.Fatalf("remote.upstream.fetch = %q, want %q", got, wantFetch)
	}

	goStdout, goStderr, goCode := runWT(t, f.base, "go", "feat-b")
	if goCode != 0 {
		t.Fatalf("wt go feat-b after --remote upstream adopt: exit=%d stderr=%q", goCode, goStderr)
	}
	slotPath := assertSingleLinePath(t, goStdout)

	if upstream := runGit(t, slotPath, "rev-parse", "--abbrev-ref", "@{u}"); upstream != "upstream/feat-b" {
		t.Fatalf("slot upstream = %q, want upstream/feat-b", upstream)
	}

	listOut, listStderr, listCode := runWT(t, f.base, "list")
	if listCode != 0 {
		t.Fatalf("wt list after --remote upstream adopt: exit=%d stderr=%q", listCode, listStderr)
	}

	for _, line := range strings.Split(listOut, "\n") {
		if strings.Contains(line, "feat-b") && !strings.Contains(line, "clean") {
			t.Fatalf("feat-b's slot is not reported clean after --remote upstream adopt:\n%s", listOut)
		}
	}
}

func TestAdoptRefusesUnsupportedTargets(t *testing.T) {
	t.Run("bare-as-root", func(t *testing.T) {
		remote := newRemote(t)
		base := t.TempDir()
		runGit(t, base, "clone", "--bare", remote, base)

		_, stderr, code := runWT(t, base, "adopt")
		if code != 1 {
			t.Fatalf("wt adopt on a bare-as-root repo: exit=%d, want 1; stderr=%q", code, stderr)
		}
		if !strings.Contains(stderr, "docs/adopting-existing-repos.md") {
			t.Fatalf("stderr does not point at the manual recipe:\n%s", stderr)
		}
	})

	t.Run("not a repo at all", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi\n"), 0o644); err != nil {
			t.Fatalf("writing notes.txt: %v", err)
		}

		_, stderr, code := runWT(t, dir, "adopt")
		if code != 1 {
			t.Fatalf("wt adopt on a non-repo directory: exit=%d, want 1; stderr=%q", code, stderr)
		}
	})

	t.Run("normal non-bare clone", func(t *testing.T) {
		remote := newRemote(t)
		dir := t.TempDir()
		runGit(t, dir, "clone", remote, ".")

		_, stderr, code := runWT(t, dir, "adopt")
		if code != 1 {
			t.Fatalf("wt adopt on a normal clone: exit=%d, want 1; stderr=%q", code, stderr)
		}
	})
}
