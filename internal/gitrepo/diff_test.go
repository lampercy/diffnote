package gitrepo

import "testing"

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
