package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestCommentsRecoverByPatchIDAndMoveOnSave(t *testing.T) {
	reviewStore, err := Open(filepath.Join(t.TempDir(), "reviews.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reviewStore.Close()

	comment := Comment{
		ID:       "comment-1",
		FilePath: "main.go",
		Side:     "new",
		Line:     12,
		Context:  "+changed",
		Body:     "Handle the error.",
	}
	empty, err := reviewStore.Comments("repo", "old-hash", "same-patch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviewStore.Save("repo", "old-hash", "same-patch", empty.Revision, []Comment{comment}); err != nil {
		t.Fatal(err)
	}

	recovered, err := reviewStore.Comments("repo", "new-hash", "same-patch")
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.Recovered || len(recovered.Comments) != 1 {
		t.Fatalf("unexpected recovered comments: %#v", recovered)
	}
	if _, err := reviewStore.Save("repo", "new-hash", "same-patch", recovered.Revision, recovered.Comments); err != nil {
		t.Fatal(err)
	}

	exact, err := reviewStore.Comments("repo", "new-hash", "same-patch")
	if err != nil {
		t.Fatal(err)
	}
	if exact.Recovered || len(exact.Comments) != 1 || exact.Comments[0].CommitHash != "new-hash" {
		t.Fatalf("comments did not move to rebased commit: %#v", exact)
	}
	if _, err := reviewStore.Save("repo", "newer-hash", "same-patch", exact.Revision, nil); err != nil {
		t.Fatal(err)
	}
	deleted, err := reviewStore.Comments("repo", "newer-hash", "same-patch")
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted.Comments) != 0 {
		t.Fatalf("deleted recovered comment returned: %#v", deleted)
	}
}

func TestSaveRejectsInvalidComment(t *testing.T) {
	reviewStore, err := Open(filepath.Join(t.TempDir(), "reviews.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reviewStore.Close()

	initial, err := reviewStore.Comments("repo", "hash", "patch")
	if err != nil {
		t.Fatal(err)
	}
	_, err = reviewStore.Save("repo", "hash", "patch", initial.Revision, []Comment{{ID: "id", FilePath: "x", Side: "both", Line: 1, Body: "body"}})
	if err == nil {
		t.Fatal("expected invalid side error")
	}
}

func TestSaveRejectsStaleRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reviews.db")
	reviewStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reviewStore.Close()
	secondStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer secondStore.Close()

	initial, err := reviewStore.Comments("repo", "hash", "patch")
	if err != nil {
		t.Fatal(err)
	}
	comment := Comment{ID: "one", FilePath: "x", Side: "new", Line: 1, Context: "+x", Body: "first"}
	if _, err := reviewStore.Save("repo", "hash", "patch", initial.Revision, []Comment{comment}); err != nil {
		t.Fatal(err)
	}
	comment.ID = "two"
	if _, err := secondStore.Save("repo", "hash", "patch", initial.Revision, []Comment{comment}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale save error = %v, want revision conflict", err)
	}
	current, err := reviewStore.Comments("repo", "hash", "patch")
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Comments) != 1 || current.Comments[0].ID != "one" {
		t.Fatalf("stale save changed review: %#v", current.Comments)
	}
}

func TestProjectsRememberSelection(t *testing.T) {
	reviewStore, err := Open(filepath.Join(t.TempDir(), "reviews.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reviewStore.Close()
	if err := reviewStore.SelectProject("/first", "first"); err != nil {
		t.Fatal(err)
	}
	if err := reviewStore.SelectProject("/second", "second"); err != nil {
		t.Fatal(err)
	}
	projects, err := reviewStore.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 || !projects[0].Selected || projects[0].Path != "/second" || projects[1].Selected {
		t.Fatalf("unexpected projects: %#v", projects)
	}
}
