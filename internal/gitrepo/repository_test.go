package gitrepo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRepositoryReadsBranchesCommitsAndDiff(t *testing.T) {
	root := newTestRepository(t)
	writeFile(t, root, "message.txt", "one\ntwo\n")
	git(t, root, "add", "message.txt")
	git(t, root, "commit", "-m", "initial")
	git(t, root, "branch", "feature/local")

	writeFile(t, root, "message.txt", "one\nchanged\nthree\n")
	git(t, root, "add", "message.txt")
	git(t, root, "commit", "-m", "change message")

	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := Open(nested)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Root != root || repo.CurrentBranch != "main" {
		t.Fatalf("unexpected repository: %#v", repo)
	}

	branches, err := repo.Branches()
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 2 {
		t.Fatalf("got %d branches, want 2", len(branches))
	}
	for _, branch := range branches {
		if branch.Name == "feature/local" && branch.Remote {
			t.Fatal("local branch containing slash marked remote")
		}
	}

	commits, err := repo.Commits("main", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits[0].Subject != "change message" {
		t.Fatalf("unexpected commits: %#v", commits)
	}
	count, err := repo.CommitCount("main")
	if err != nil || count != 2 {
		t.Fatalf("commit count = %d, %v; want 2", count, err)
	}
	secondPage, err := repo.CommitPage("main", 1, 1)
	if err != nil || len(secondPage) != 1 || secondPage[0].Subject != "initial" {
		t.Fatalf("unexpected paginated commits: %#v, %v", secondPage, err)
	}
	matches, err := repo.CommitPageSearch("main", 50, 0, "change message")
	if err != nil || len(matches) != 1 || matches[0].Subject != "change message" {
		t.Fatalf("unexpected commit search: %#v, %v", matches, err)
	}
	matchCount, err := repo.CommitCountSearch("main", "change message")
	if err != nil || matchCount != 1 {
		t.Fatalf("commit search count = %d, %v; want 1", matchCount, err)
	}

	detail, err := repo.Commit(commits[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if detail.PatchID == "" || len(detail.Files) != 1 || detail.Files[0].NewPath != "message.txt" {
		t.Fatalf("unexpected commit detail: %#v", detail)
	}
	lines := detail.Files[0].Hunks[0].Lines
	if len(lines) < 4 {
		t.Fatalf("diff is unexpectedly short: %#v", lines)
	}
}

func TestPatchIDSurvivesRebase(t *testing.T) {
	root := newTestRepository(t)
	writeFile(t, root, "base.txt", "base\n")
	git(t, root, "add", "base.txt")
	git(t, root, "commit", "-m", "base")
	base := git(t, root, "rev-parse", "HEAD")

	writeFile(t, root, "feature.txt", "feature\n")
	git(t, root, "add", "feature.txt")
	git(t, root, "commit", "-m", "feature")
	original := git(t, root, "rev-parse", "HEAD")

	git(t, root, "checkout", "-b", "rebased", base)
	writeFile(t, root, "unrelated.txt", "unrelated\n")
	git(t, root, "add", "unrelated.txt")
	git(t, root, "commit", "-m", "unrelated")
	git(t, root, "cherry-pick", original)
	rebased := git(t, root, "rev-parse", "HEAD")

	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	originalID, err := repo.PatchID(original)
	if err != nil {
		t.Fatal(err)
	}
	rebasedID, err := repo.PatchID(rebased)
	if err != nil {
		t.Fatal(err)
	}
	if original == rebased || originalID == "" || originalID != rebasedID {
		t.Fatalf("patch IDs differ after rebase: %q and %q", originalID, rebasedID)
	}
}

func TestPatchIDSurvivesLineShift(t *testing.T) {
	root := newTestRepository(t)
	writeFile(t, root, "lines.txt", "one\ntwo\nthree\nfour\nfive\nsix\nseven\n")
	git(t, root, "add", "lines.txt")
	git(t, root, "commit", "-m", "base")
	base := git(t, root, "rev-parse", "HEAD")

	writeFile(t, root, "lines.txt", "one\ntwo\nthree\nfour\nchanged\nsix\nseven\n")
	git(t, root, "add", "lines.txt")
	git(t, root, "commit", "-m", "change fifth line")
	original := git(t, root, "rev-parse", "HEAD")

	git(t, root, "checkout", "-b", "shifted", base)
	writeFile(t, root, "lines.txt", "inserted\none\ntwo\nthree\nfour\nfive\nsix\nseven\n")
	git(t, root, "add", "lines.txt")
	git(t, root, "commit", "-m", "insert earlier line")
	git(t, root, "cherry-pick", original)
	rebased := git(t, root, "rev-parse", "HEAD")

	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	originalID, err := repo.PatchID(original)
	if err != nil {
		t.Fatal(err)
	}
	rebasedID, err := repo.PatchID(rebased)
	if err != nil {
		t.Fatal(err)
	}
	if originalID != rebasedID {
		t.Fatalf("strict patch IDs differ after line shift: %q and %q", originalID, rebasedID)
	}
}

func TestMergeCommitUsesFirstParentDiff(t *testing.T) {
	root := newTestRepository(t)
	writeFile(t, root, "base.txt", "base\n")
	git(t, root, "add", "base.txt")
	git(t, root, "commit", "-m", "base")
	git(t, root, "checkout", "-b", "feature")
	writeFile(t, root, "feature.txt", "feature\n")
	git(t, root, "add", "feature.txt")
	git(t, root, "commit", "-m", "feature")
	git(t, root, "checkout", "main")
	writeFile(t, root, "main.txt", "main\n")
	git(t, root, "add", "main.txt")
	git(t, root, "commit", "-m", "main")
	git(t, root, "merge", "--no-ff", "feature", "-m", "merge feature")
	hash := git(t, root, "rev-parse", "HEAD")

	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := repo.Commit(hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Files) != 1 || detail.Files[0].NewPath != "feature.txt" || detail.PatchID == "" {
		t.Fatalf("unexpected merge diff: %#v", detail)
	}
}

func TestCommitDoesNotRunTextconv(t *testing.T) {
	root := newTestRepository(t)
	sentinel := filepath.Join(root, "textconv-ran")
	script := filepath.Join(root, "textconv.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch \""+sentinel+"\"\ncat \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, ".gitattributes", "*.secret diff=evil\n")
	writeFile(t, root, "value.secret", "before\n")
	git(t, root, "add", ".gitattributes", "value.secret")
	git(t, root, "commit", "-m", "initial")
	git(t, root, "config", "diff.evil.textconv", script)
	writeFile(t, root, "value.secret", "after\n")
	git(t, root, "add", "value.secret")
	git(t, root, "commit", "-m", "change secret")

	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(git(t, root, "rev-parse", "HEAD")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("repository textconv executed; stat error=%v", err)
	}
}

func TestCanonicalPatchIDPreservesWhitespaceAndContext(t *testing.T) {
	indented := canonicalPatchID([]byte("diff --git a/a b/a\n@@ -1 +1 @@ first\n-old\n+    return 1\n"))
	unindented := canonicalPatchID([]byte("diff --git a/a b/a\n@@ -1 +1 @@ first\n-old\n+return 1\n"))
	if indented == unindented {
		t.Fatal("canonical patch ID ignored changed-line whitespace")
	}
	otherContext := canonicalPatchID([]byte("diff --git a/a b/a\n@@ -20 +20 @@ second\n-old\n+    return 1\n"))
	if indented == otherContext {
		t.Fatal("canonical patch ID ignored hunk context")
	}
}

func TestLimitedBufferBoundsStoredOutput(t *testing.T) {
	buffer := limitedBuffer{limit: 4}
	written, err := buffer.Write([]byte("123456"))
	if !errors.Is(err, errOutputLimit) || written != 4 || !buffer.exceeded || buffer.String() != "1234" {
		t.Fatalf("unexpected bounded write: written=%d err=%v exceeded=%v value=%q", written, err, buffer.exceeded, buffer.String())
	}
}

func TestEmptyRepositoryHasNoCommits(t *testing.T) {
	root := newTestRepository(t)
	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := repo.Commits("HEAD", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 0 {
		t.Fatalf("got %d commits, want none", len(commits))
	}
}

func TestLinkedWorktreesHaveDistinctContextIDs(t *testing.T) {
	root := newTestRepository(t)
	writeFile(t, root, "file.txt", "content\n")
	git(t, root, "add", "file.txt")
	git(t, root, "commit", "-m", "initial")
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, root, "worktree", "add", "-b", "linked", linked)
	first, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(linked)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.ContextID == second.ContextID {
		t.Fatalf("unexpected worktree identities: first=%#v second=%#v", first, second)
	}
}

func TestMatchRemoteNameUsesLongestConfiguredPrefix(t *testing.T) {
	if got := matchRemoteName("team/origin/main", []string{"team", "team/origin"}); got != "team/origin" {
		t.Fatalf("remote name = %q, want team/origin", got)
	}
}

func newTestRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-b", "main")
	git(t, root, "config", "user.name", "DiffNote Test")
	git(t, root, "config", "user.email", "diffnote@example.com")
	return root
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(trimSpace(output))
}

func trimSpace(value []byte) []byte {
	start, end := 0, len(value)
	for start < end && (value[start] == '\n' || value[start] == '\r' || value[start] == ' ') {
		start++
	}
	for end > start && (value[end-1] == '\n' || value[end-1] == '\r' || value[end-1] == ' ') {
		end--
	}
	return value[start:end]
}
