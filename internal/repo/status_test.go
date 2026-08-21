package repo

import "testing"

type parseQuickStatusTest struct {
	Name   string
	Output string
	Want   WorktreeStatus
}

var parseQuickStatusTests = []parseQuickStatusTest{
	{
		Name:   "empty output",
		Output: "",
		Want:   WorktreeStatus{},
	},
	{
		Name: "detached header only",
		Output: "# branch.oid abc123\n" +
			"# branch.head (detached)\n",
		Want: WorktreeStatus{},
	},
	{
		Name: "branch with no upstream",
		Output: "# branch.oid abc123\n" +
			"# branch.head main\n",
		Want: WorktreeStatus{},
	},
	{
		Name: "branch with upstream in sync",
		Output: "# branch.oid abc123\n" +
			"# branch.head main\n" +
			"# branch.upstream origin/main\n" +
			"# branch.ab +0 -0\n",
		Want: WorktreeStatus{HasUpstream: true},
	},
	{
		Name: "branch with upstream and two ahead",
		Output: "# branch.oid abc123\n" +
			"# branch.head main\n" +
			"# branch.upstream origin/main\n" +
			"# branch.ab +2 -0\n",
		Want: WorktreeStatus{HasUpstream: true, Ahead: 2},
	},
	{
		Name: "modified file",
		Output: "# branch.oid abc123\n" +
			"# branch.head main\n" +
			"# branch.upstream origin/main\n" +
			"# branch.ab +0 -0\n" +
			"1 .M N... 100644 100644 100644 abcd1234 abcd1234 file.txt\n",
		Want: WorktreeStatus{Dirty: true, HasUpstream: true},
	},
	{
		Name: "untracked file",
		Output: "# branch.oid abc123\n" +
			"# branch.head main\n" +
			"? scratch.txt\n",
		Want: WorktreeStatus{Dirty: true},
	},
	{
		Name: "renamed entry",
		Output: "# branch.oid abc123\n" +
			"# branch.head main\n" +
			"2 R100 N... 100644 100644 100644 abcd1234 abcd1234 R100 new.txt\told.txt\n",
		Want: WorktreeStatus{Dirty: true},
	},
	{
		Name: "unmerged entry",
		Output: "# branch.oid abc123\n" +
			"# branch.head main\n" +
			"u UU N... 100644 100644 100644 100644 aaaa bbbb cccc conflict.txt\n",
		Want: WorktreeStatus{Dirty: true},
	},
	{
		Name: "ignored entry tolerated",
		Output: "# branch.oid abc123\n" +
			"# branch.head main\n" +
			"! build/output.o\n",
		Want: WorktreeStatus{Dirty: true},
	},
}

func TestParseQuickStatus(t *testing.T) {
	for _, tc := range parseQuickStatusTests {
		t.Run(tc.Name, func(t *testing.T) {
			got := parseQuickStatus(tc.Output)
			if got != tc.Want {
				t.Fatalf("parseQuickStatus(%q) = %+v, want %+v", tc.Output, got, tc.Want)
			}
		})
	}
}
