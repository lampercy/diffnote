package gitrepo

import (
	"strings"
	"testing"
)

func TestDiffRecordBudget(t *testing.T) {
	// Less than one MiB of Git output must not create millions of objects.
	_, err := ParseUnifiedDiff("diff --git a/a b/a\n@@ -0,0 +1,300000 @@\n" + strings.Repeat("+\n", maxDiffRecords))
	if err == nil || !strings.Contains(err.Error(), "review a smaller commit") {
		t.Fatalf("expected bounded diff error, got %v", err)
	}
}

func TestDiffSeparatorsAndMarkerLikeSource(t *testing.T) {
	files, err := ParseUnifiedDiff("diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1,2 +1,2 @@\n--- removed\n+++ added\n \n\\ No newline at end of file\n\n")
	if err != nil {
		t.Fatal(err)
	}
	file := files[0]
	if file.OldPath != "a" || file.NewPath != "a" || len(file.Hunks[0].Lines) != 4 {
		t.Fatalf("source markers or separator parsed incorrectly: %#v", file)
	}
	lines := file.Hunks[0].Lines
	assertLine(t, lines[0], "delete", 1, 0)
	assertLine(t, lines[1], "add", 0, 1)
	assertLine(t, lines[2], "context", 2, 2)
	assertLine(t, lines[3], "meta", 0, 0)
}

func TestPatchIdentityShortLinesAllocation(t *testing.T) {
	patch := []byte(strings.Repeat("+\n", 300_000))
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			canonicalPatchID(patch)
		}
	})
	if result.AllocedBytesPerOp() > 64*1024 {
		t.Fatalf("patch identity allocated %d bytes for short lines", result.AllocedBytesPerOp())
	}
}

func TestParseUnifiedDiff(t *testing.T) {
	diff := `diff --git a/old name.txt b/new name.txt
similarity index 80%
rename from old name.txt
rename to new name.txt
--- a/old name.txt
+++ b/new name.txt
@@ -1,3 +1,4 @@
 alpha
-before
+after
+extra
 omega
diff --git a/image.png b/image.png
new file mode 100644
Binary files /dev/null and b/image.png differ
`

	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	file := files[0]
	if file.Status != "renamed" || file.OldPath != "old name.txt" || file.NewPath != "new name.txt" {
		t.Fatalf("unexpected rename: %#v", file)
	}
	if file.Additions != 2 || file.Deletions != 1 {
		t.Fatalf("unexpected file statistics: +%d -%d", file.Additions, file.Deletions)
	}
	lines := file.Hunks[0].Lines
	assertLine(t, lines[0], "context", 1, 1)
	assertLine(t, lines[1], "delete", 2, 0)
	assertLine(t, lines[2], "add", 0, 2)
	assertLine(t, lines[3], "add", 0, 3)
	assertLine(t, lines[4], "context", 3, 4)
	if !files[1].Binary || files[1].Status != "added" {
		t.Fatalf("unexpected binary file: %#v", files[1])
	}
}

func TestParseUnifiedDiffRejectsMalformedHunk(t *testing.T) {
	_, err := ParseUnifiedDiff("diff --git a/a b/a\n@@ malformed @@\n")
	if err == nil {
		t.Fatal("expected malformed hunk error")
	}
}

func TestParseUnifiedDiffDecodesQuotedPaths(t *testing.T) {
	diff := "diff --git \"a/a\\tb.txt\" \"b/a\\tb.txt\"\n" +
		"--- \"a/a\\tb.txt\"\n+++ \"b/a\\tb.txt\"\n@@ -1 +1 @@\n-old\n+new\n"
	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].OldPath != "a\tb.txt" || files[0].NewPath != "a\tb.txt" {
		t.Fatalf("quoted paths were not decoded: %#v", files)
	}
}

func assertLine(t *testing.T, line DiffLine, kind string, oldLine, newLine int) {
	t.Helper()
	if line.Kind != kind || value(line.OldLine) != oldLine || value(line.NewLine) != newLine {
		t.Fatalf("got %#v, want kind=%s old=%d new=%d", line, kind, oldLine, newLine)
	}
}

func value(number *int) int {
	if number == nil {
		return 0
	}
	return *number
}
