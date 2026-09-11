package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/percy/diffnote/internal/gitrepo"
	"github.com/percy/diffnote/internal/store"
)

//go:embed web/*
var webFiles embed.FS

type server struct {
	repo     *gitrepo.Repository
	repoMu   sync.RWMutex
	switchMu sync.Mutex
	store    *store.Store
	web      http.Handler
	gitSlots chan struct{}
}

type stateResponse struct {
	Repository *gitrepo.Repository `json:"repository"`
	ProjectID  string              `json:"projectId"`
	Branches   []gitrepo.Branch    `json:"branches"`
}

type commitsResponse struct {
	Commits []gitrepo.Commit `json:"commits"`
	Total   int              `json:"total"`
	Tip     string           `json:"tip"`
}

type commentsResponse struct {
	Comments  []store.Comment `json:"comments"`
	Orphans   []store.Comment `json:"orphans"`
	Recovered bool            `json:"recovered"`
	Revision  string          `json:"revision"`
}

type saveCommentsRequest struct {
	Comments []store.Comment `json:"comments"`
	Revision string          `json:"revision"`
}

type viewedRequest struct {
	FileKey string `json:"fileKey"`
	Viewed  bool   `json:"viewed"`
}

type projectRequest struct {
	Path string `json:"path"`
}

type directoriesResponse struct {
	Home        string   `json:"home"`
	Directories []string `json:"directories"`
}

type appSettings struct {
	Font           string `json:"font"`
	FontSize       int    `json:"fontSize"`
	LineHeight     int    `json:"lineHeight"`
	Theme          string `json:"theme"`
	WrapLines      bool   `json:"wrapLines"`
	SidebarWidth   int    `json:"sidebarWidth"`
	ContextLines   int    `json:"contextLines"`
	CollapseViewed bool   `json:"collapseViewed"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "diffnote:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("diffnote", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	addr := flags.String("addr", "127.0.0.1:0", "loopback address to listen on")
	noBrowser := flags.Bool("no-browser", false, "do not open a browser")
	database := flags.String("database", "", "review database path")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: diffnote [options]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return errors.New("repository paths are added from the web interface")
	}

	reviewStore, err := store.Open(*database)
	if err != nil {
		return err
	}
	defer reviewStore.Close()
	var repo *gitrepo.Repository
	projects, err := reviewStore.Projects()
	if err != nil {
		return err
	}
	for _, project := range projects {
		if project.Selected {
			repo, err = gitrepo.Open(project.Path)
			if err != nil {
				log.Printf("selected project is unavailable: %v", err)
			}
			break
		}
	}

	staticFiles, err := fs.Sub(webFiles, "web")
	if err != nil {
		return err
	}
	app := &server{repo: repo, store: reviewStore, web: http.FileServer(http.FS(staticFiles)), gitSlots: make(chan struct{}, 4)}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !tcpAddress.IP.IsLoopback() {
		listener.Close()
		return errors.New("refusing to listen on a non-loopback address")
	}
	httpServer := &http.Server{
		Handler:           app.routes(listener.Addr().String()),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	url := "http://" + listener.Addr().String()
	if repo == nil {
		log.Printf("DiffNote is ready at %s; add a project in the browser", url)
	} else {
		log.Printf("reviewing %s at %s", repo.Root, url)
	}
	if !*noBrowser {
		go openBrowser(url)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpServer.Shutdown(ctx)
	}()

	err = httpServer.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *server) routes(allowedHost string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/commits", s.handleCommits)
	mux.HandleFunc("GET /api/commits/{hash}", s.handleCommit)
	mux.HandleFunc("GET /api/comments/{hash}", s.handleComments)
	mux.HandleFunc("PUT /api/comments/{hash}", s.handleSaveComments)
	mux.HandleFunc("GET /api/viewed/{hash}", s.handleViewedFiles)
	mux.HandleFunc("PUT /api/viewed/{hash}", s.handleSetViewed)
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("PUT /api/settings", s.handleSaveSettings)
	mux.HandleFunc("GET /api/projects", s.handleProjects)
	mux.HandleFunc("POST /api/projects", s.handleSelectProject)
	mux.HandleFunc("PUT /api/projects/select", s.handleSelectProject)
	mux.HandleFunc("GET /api/directories", s.handleDirectories)
	mux.Handle("/", s.web)
	return securityHeaders(mux, allowedHost)
}

func (s *server) handleDirectories(w http.ResponseWriter, request *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	value := strings.TrimSpace(request.URL.Query().Get("path"))
	if value == "" || value == "~" {
		value = home
	} else if strings.HasPrefix(value, "~/") {
		value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	value, err = filepath.Abs(value)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid directory path"))
		return
	}
	directory := value
	prefix := ""
	if info, statErr := os.Stat(value); statErr != nil || !info.IsDir() {
		directory = filepath.Dir(value)
		prefix = filepath.Base(value)
	}
	handle, err := os.Open(directory)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("read directory: %w", err))
		return
	}
	defer handle.Close()
	directories := []string{}
	for scanned := 0; scanned < 2_000 && len(directories) < 200; {
		entries, readErr := handle.ReadDir(100)
		scanned += len(entries)
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), prefix) {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			info, statErr := os.Stat(path)
			if statErr == nil && info.IsDir() {
				directories = append(directories, path)
				if len(directories) == 200 {
					break
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("read directory: %w", readErr))
			return
		}
	}
	writeJSON(w, http.StatusOK, directoriesResponse{Home: home, Directories: directories})
}

func (s *server) handleProjects(w http.ResponseWriter, _ *http.Request) {
	projects, err := s.store.Projects()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *server) handleSelectProject(w http.ResponseWriter, request *http.Request) {
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	request.Body = http.MaxBytesReader(w, request.Body, 16<<10)
	var payload projectRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || strings.TrimSpace(payload.Path) == "" {
		writeError(w, http.StatusBadRequest, errors.New("invalid project path"))
		return
	}
	if !s.acquireGit(request.Context()) {
		return
	}
	defer s.releaseGit()
	repo, err := gitrepo.Open(strings.TrimSpace(payload.Path))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.SelectProject(repo.Root, repo.Name); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.repoMu.Lock()
	s.repo = repo
	s.repoMu.Unlock()
	writeJSON(w, http.StatusOK, repo)
}

func (s *server) currentRepository(request *http.Request) (*gitrepo.Repository, error) {
	s.repoMu.RLock()
	defer s.repoMu.RUnlock()
	if s.repo == nil {
		return nil, errors.New("no project selected")
	}
	if expected := request.Header.Get("X-DiffNote-Project"); expected != "" && expected != s.repo.ContextID {
		return nil, errors.New("selected project changed; reload the page")
	}
	return s.repo, nil
}

func (s *server) handleSettings(w http.ResponseWriter, _ *http.Request) {
	value, err := s.store.Settings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(value))
}

func (s *server) handleSaveSettings(w http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(w, request.Body, 16<<10)
	var settings appSettings
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil || !validSettings(settings) {
		writeError(w, http.StatusBadRequest, errors.New("invalid settings payload"))
		return
	}
	value, err := json.Marshal(settings)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.SaveSettings(string(value)); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validSettings(settings appSettings) bool {
	validFont := settings.Font == "github" || settings.Font == "system" || settings.Font == "jetbrains" || settings.Font == "fira"
	validTheme := settings.Theme == "system" || settings.Theme == "light" || settings.Theme == "dark"
	validContext := settings.ContextLines == 0 || settings.ContextLines == 3 || settings.ContextLines == 10 || settings.ContextLines == 20 || settings.ContextLines == 50
	return validFont && validTheme && validContext && settings.FontSize >= 12 && settings.FontSize <= 20 &&
		settings.LineHeight >= 18 && settings.LineHeight <= 34 && settings.SidebarWidth >= 260 && settings.SidebarWidth <= 500
}

func (s *server) handleViewedFiles(w http.ResponseWriter, request *http.Request) {
	repo, err := s.currentRepository(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	files, err := s.store.ViewedFiles(repo.ID, request.PathValue("hash"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, files)
}

func (s *server) handleSetViewed(w http.ResponseWriter, request *http.Request) {
	repo, err := s.currentRepository(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, 16<<10)
	var payload viewedRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid viewed payload"))
		return
	}
	if err := s.store.SetViewed(repo.ID, request.PathValue("hash"), payload.FileKey, payload.Viewed); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleState(w http.ResponseWriter, request *http.Request) {
	repo, err := s.currentRepository(request)
	if err != nil {
		writeJSON(w, http.StatusOK, stateResponse{Branches: []gitrepo.Branch{}})
		return
	}
	if !s.acquireGit(request.Context()) {
		return
	}
	defer s.releaseGit()
	branches, err := repo.Branches()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, stateResponse{Repository: repo, ProjectID: repo.ContextID, Branches: branches})
}

func (s *server) handleCommits(w http.ResponseWriter, request *http.Request) {
	repo, err := s.currentRepository(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !s.acquireGit(request.Context()) {
		return
	}
	defer s.releaseGit()
	offset := 0
	if value := request.URL.Query().Get("offset"); value != "" {
		var parseErr error
		offset, parseErr = strconv.Atoi(value)
		if parseErr != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, errors.New("invalid commit offset"))
			return
		}
	}
	ref := request.URL.Query().Get("tip")
	search := strings.TrimSpace(request.URL.Query().Get("search"))
	if len(search) > 500 {
		writeError(w, http.StatusBadRequest, errors.New("commit search is too long"))
		return
	}
	if ref == "" {
		ref = request.URL.Query().Get("ref")
		ref, err = repo.ResolveCommit(ref)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if ref == "" {
			writeJSON(w, http.StatusOK, commitsResponse{Commits: []gitrepo.Commit{}})
			return
		}
	}
	commits, err := repo.CommitPageSearch(ref, 50, offset, search)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	total := 0
	if offset == 0 {
		total, err = repo.CommitCountSearch(ref, search)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, commitsResponse{Commits: commits, Total: total, Tip: ref})
}

func (s *server) handleCommit(w http.ResponseWriter, request *http.Request) {
	repo, err := s.currentRepository(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !s.acquireGit(request.Context()) {
		return
	}
	defer s.releaseGit()
	contextLines := 3
	if value := request.URL.Query().Get("context"); value != "" {
		parsedContext, parseErr := strconv.Atoi(value)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, errors.New("invalid diff context"))
			return
		}
		contextLines = parsedContext
	}
	commit, err := repo.CommitWithContext(request.PathValue("hash"), contextLines)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, commit)
}

func (s *server) handleComments(w http.ResponseWriter, request *http.Request) {
	repo, err := s.currentRepository(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	hash := request.PathValue("hash")
	if !s.acquireGit(request.Context()) {
		return
	}
	detail, err := repo.CommitWithContext(hash, 100)
	s.releaseGit()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.store.Comments(repo.ID, hash, detail.PatchID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	comments, orphans := anchorComments(result.Comments, detail.Files, result.Recovered)
	writeJSON(w, http.StatusOK, commentsResponse{Comments: comments, Orphans: orphans, Recovered: result.Recovered, Revision: result.Revision})
}

func (s *server) handleSaveComments(w http.ResponseWriter, request *http.Request) {
	repo, err := s.currentRepository(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	hash := request.PathValue("hash")
	if !s.acquireGit(request.Context()) {
		return
	}
	patchID, err := repo.PatchID(hash)
	s.releaseGit()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, 2<<20)
	var payload saveCommentsRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.Revision == "" {
		writeError(w, http.StatusBadRequest, errors.New("invalid comments payload"))
		return
	}
	comments := payload.Comments
	if len(comments) > 1000 {
		writeError(w, http.StatusBadRequest, errors.New("too many comments"))
		return
	}
	for index := range comments {
		comments[index].Body = strings.TrimSpace(comments[index].Body)
		if len(comments[index].Body) > 20_000 || len(comments[index].Context) > 20_000 {
			writeError(w, http.StatusBadRequest, errors.New("comment is too large"))
			return
		}
	}
	newRevision, err := s.store.Save(repo.ID, hash, patchID, payload.Revision, comments)
	if err != nil {
		if errors.Is(err, store.ErrRevisionConflict) {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"revision": newRevision})
}

func (s *server) acquireGit(ctx context.Context) bool {
	if s.gitSlots == nil {
		return true
	}
	select {
	case s.gitSlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *server) releaseGit() {
	if s.gitSlots != nil {
		<-s.gitSlots
	}
}

func anchorComments(comments []store.Comment, files []gitrepo.FileDiff, allowRemap bool) ([]store.Comment, []store.Comment) {
	attached := []store.Comment{}
	orphans := []store.Comment{}
	for _, comment := range comments {
		matches := commentAnchors(comment, files)
		exact := -1
		for index, match := range matches {
			if match == comment.Line {
				exact = index
				break
			}
		}
		if exact >= 0 {
			attached = append(attached, comment)
		} else if allowRemap && len(matches) == 1 {
			comment.Line = matches[0]
			attached = append(attached, comment)
		} else {
			orphans = append(orphans, comment)
		}
	}
	return attached, orphans
}

func commentAnchors(comment store.Comment, files []gitrepo.FileDiff) []int {
	matches := []int{}
	for _, file := range files {
		path := file.NewPath
		if comment.Side == "old" {
			path = file.OldPath
		}
		if path != comment.FilePath {
			continue
		}
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				var number *int
				if comment.Side == "old" {
					number = line.OldLine
				} else {
					number = line.NewLine
				}
				if number != nil && line.Content == comment.Context {
					matches = append(matches, *number)
				}
			}
		}
	}
	return matches
}

func securityHeaders(next http.Handler, allowedHost string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if request.Host != allowedHost {
			writeError(w, http.StatusForbidden, errors.New("invalid host"))
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			origin := request.Header.Get("Origin")
			if origin != "" && origin != "http://"+request.Host && origin != "https://"+request.Host {
				writeError(w, http.StatusForbidden, errors.New("cross-origin request rejected"))
				return
			}
			if request.Header.Get("Content-Type") != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, errors.New("application/json required"))
				return
			}
		}
		next.ServeHTTP(w, request)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func openBrowser(url string) {
	commands := [][]string{
		{"xdg-open", url},
		{"open", url},
	}
	for _, command := range commands {
		if _, err := exec.LookPath(command[0]); err == nil {
			if err := exec.Command(command[0], command[1:]...).Start(); err == nil {
				return
			}
		}
	}
	log.Printf("open %s in a browser", url)
}

func databasePathForTest(root string) string {
	return filepath.Join(root, "reviews.db")
}
