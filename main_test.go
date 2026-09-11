package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/percy/diffnote/internal/gitrepo"
	"github.com/percy/diffnote/internal/store"
)

func TestReviewAPI(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.name", "DiffNote Test")
	runGit(t, root, "config", "user.email", "diffnote@example.com")
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "hello.txt")
	runGit(t, root, "commit", "-m", "add greeting")
	hash := runGit(t, root, "rev-parse", "HEAD")

	repo, err := gitrepo.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	reviewStore, err := store.Open(databasePathForTest(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer reviewStore.Close()
	staticFiles, err := fs.Sub(webFiles, "web")
	if err != nil {
		t.Fatal(err)
	}
	app := (&server{repo: repo, store: reviewStore, web: http.FileServer(http.FS(staticFiles)), gitSlots: make(chan struct{}, 2)}).routes("example.com")

	stateRecorder := serve(t, app, http.MethodGet, "/api/state", nil, "")
	if stateRecorder.Code != http.StatusOK {
		t.Fatalf("state returned %d: %s", stateRecorder.Code, stateRecorder.Body.String())
	}
	var state stateResponse
	if err := json.Unmarshal(stateRecorder.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Repository.Root != root || len(state.Branches) != 1 {
		t.Fatalf("unexpected state: %#v", state)
	}
	projectPayload := []byte(fmt.Sprintf(`{"path":%q}`, root))
	projectSave := serve(t, app, http.MethodPost, "/api/projects", projectPayload, "")
	if projectSave.Code != http.StatusOK {
		t.Fatalf("project save returned %d: %s", projectSave.Code, projectSave.Body.String())
	}
	projects := serve(t, app, http.MethodGet, "/api/projects", nil, "")
	if projects.Code != http.StatusOK || !bytes.Contains(projects.Body.Bytes(), []byte(root)) {
		t.Fatalf("projects returned %d: %s", projects.Code, projects.Body.String())
	}

	commitRecorder := serve(t, app, http.MethodGet, "/api/commits/"+hash, nil, "")
	if commitRecorder.Code != http.StatusOK {
		t.Fatalf("commit returned %d: %s", commitRecorder.Code, commitRecorder.Body.String())
	}
	staleProjectRequest := httptest.NewRequest(http.MethodGet, "/api/commits/"+hash, nil)
	staleProjectRequest.Header.Set("X-DiffNote-Project", "different-project")
	staleProject := httptest.NewRecorder()
	app.ServeHTTP(staleProject, staleProjectRequest)
	if staleProject.Code != http.StatusBadRequest {
		t.Fatalf("stale project request returned %d, want 400", staleProject.Code)
	}

	emptyReview := serve(t, app, http.MethodGet, "/api/comments/"+hash, nil, "")
	if emptyReview.Code != http.StatusOK {
		t.Fatalf("initial comments returned %d: %s", emptyReview.Code, emptyReview.Body.String())
	}
	var initial commentsResponse
	if err := json.Unmarshal(emptyReview.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	payload := []byte(fmt.Sprintf(`{"revision":%q,"comments":[{
		"id":"review-1","filePath":"hello.txt","side":"new","line":1,
		"context":"+hello","body":"Use a more specific greeting."
	}]}`, initial.Revision))
	blocked := serve(t, app, http.MethodPut, "/api/comments/"+hash, payload, "https://evil.example")
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("cross-origin save returned %d, want 403", blocked.Code)
	}
	saved := serve(t, app, http.MethodPut, "/api/comments/"+hash, payload, "")
	if saved.Code != http.StatusOK {
		t.Fatalf("save returned %d: %s", saved.Code, saved.Body.String())
	}
	stale := serve(t, app, http.MethodPut, "/api/comments/"+hash, payload, "")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale save returned %d, want 409: %s", stale.Code, stale.Body.String())
	}
	comments := serve(t, app, http.MethodGet, "/api/comments/"+hash, nil, "")
	if comments.Code != http.StatusOK || !bytes.Contains(comments.Body.Bytes(), []byte("more specific greeting")) {
		t.Fatalf("comments returned %d: %s", comments.Code, comments.Body.String())
	}
	viewedPayload := []byte(`{"fileKey":"hello.txt","viewed":true}`)
	viewedSave := serve(t, app, http.MethodPut, "/api/viewed/"+hash, viewedPayload, "")
	if viewedSave.Code != http.StatusNoContent {
		t.Fatalf("viewed save returned %d: %s", viewedSave.Code, viewedSave.Body.String())
	}
	viewed := serve(t, app, http.MethodGet, "/api/viewed/"+hash, nil, "")
	if viewed.Code != http.StatusOK || !bytes.Contains(viewed.Body.Bytes(), []byte("hello.txt")) {
		t.Fatalf("viewed files returned %d: %s", viewed.Code, viewed.Body.String())
	}
	settingsPayload := []byte(`{"font":"github","fontSize":15,"lineHeight":24,"theme":"dark","wrapLines":true,"sidebarWidth":350,"contextLines":10,"collapseViewed":true}`)
	settingsSave := serve(t, app, http.MethodPut, "/api/settings", settingsPayload, "")
	if settingsSave.Code != http.StatusNoContent {
		t.Fatalf("settings save returned %d: %s", settingsSave.Code, settingsSave.Body.String())
	}
	settings := serve(t, app, http.MethodGet, "/api/settings", nil, "")
	if settings.Code != http.StatusOK || !bytes.Contains(settings.Body.Bytes(), []byte(`"fontSize":15`)) {
		t.Fatalf("settings returned %d: %s", settings.Code, settings.Body.String())
	}

	index := serve(t, app, http.MethodGet, "/", nil, "")
	if index.Code != http.StatusOK || index.Header().Get("Content-Security-Policy") == "" || !bytes.Contains(index.Body.Bytes(), []byte("DiffNote")) {
		t.Fatalf("index response is incomplete: status=%d", index.Code)
	}
	wrongHostRequest := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	wrongHostRequest.Host = "attacker.example"
	wrongHost := httptest.NewRecorder()
	app.ServeHTTP(wrongHost, wrongHostRequest)
	if wrongHost.Code != http.StatusForbidden {
		t.Fatalf("invalid host returned %d, want 403", wrongHost.Code)
	}
}

func TestAnchorCommentsRemapsOnlyUniqueContext(t *testing.T) {
	oldLine, firstNewLine, secondNewLine := 5, 6, 12
	files := []gitrepo.FileDiff{{
		NewPath: "main.go",
		Hunks: []gitrepo.Hunk{{Lines: []gitrepo.DiffLine{
			{Kind: "add", Content: "+unique", NewLine: &firstNewLine},
			{Kind: "add", Content: "+duplicate", NewLine: &firstNewLine},
			{Kind: "add", Content: "+duplicate", NewLine: &secondNewLine},
		}}},
	}}
	comments := []store.Comment{
		{ID: "unique", FilePath: "main.go", Side: "new", Line: oldLine, Context: "+unique", Body: "move"},
		{ID: "ambiguous", FilePath: "main.go", Side: "new", Line: oldLine, Context: "+duplicate", Body: "do not move"},
	}
	attached, orphans := anchorComments(comments, files, true)
	if len(attached) != 1 || attached[0].Line != 6 || len(orphans) != 1 || orphans[0].ID != "ambiguous" {
		t.Fatalf("unexpected anchors: attached=%#v orphans=%#v", attached, orphans)
	}
}

func serve(t *testing.T, handler http.Handler, method, target string, body []byte, origin string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func runGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(bytes.TrimSpace(output))
}
