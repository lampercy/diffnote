package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Comment struct {
	ID         string `json:"id"`
	CommitHash string `json:"commitHash"`
	PatchID    string `json:"patchId"`
	FilePath   string `json:"filePath"`
	Side       string `json:"side"`
	Line       int    `json:"line"`
	Context    string `json:"context"`
	Body       string `json:"body"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

type Result struct {
	Comments  []Comment `json:"comments"`
	Recovered bool      `json:"recovered"`
	Revision  string    `json:"revision"`
}

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

type Project struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Selected bool   `json:"selected"`
}

var ErrRevisionConflict = errors.New("review changed in another window")

func DefaultPath() (string, error) {
	root := os.Getenv("XDG_DATA_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(root, "diffnote", "reviews.db"), nil
}

func Open(path string) (*Store, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, fmt.Errorf("resolve data directory: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA foreign_keys = ON;
		CREATE TABLE IF NOT EXISTS comments (
			id TEXT PRIMARY KEY,
			repo_id TEXT NOT NULL,
			commit_hash TEXT NOT NULL,
			patch_id TEXT NOT NULL,
			file_path TEXT NOT NULL,
			side TEXT NOT NULL,
			line INTEGER NOT NULL,
			context TEXT NOT NULL,
			body TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS comments_commit ON comments(repo_id, commit_hash);
		CREATE INDEX IF NOT EXISTS comments_patch ON comments(repo_id, patch_id);
		CREATE TABLE IF NOT EXISTS review_revisions (
			repo_id TEXT NOT NULL,
			patch_id TEXT NOT NULL,
			revision TEXT NOT NULL,
			PRIMARY KEY (repo_id, patch_id)
		);
		CREATE TABLE IF NOT EXISTS viewed_files (
			repo_id TEXT NOT NULL,
			commit_hash TEXT NOT NULL,
			file_key TEXT NOT NULL,
			PRIMARY KEY (repo_id, commit_hash, file_key)
		);
		CREATE TABLE IF NOT EXISTS app_settings (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			value TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS projects (
			path TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			selected INTEGER NOT NULL DEFAULT 0,
			added_at TEXT NOT NULL
		);
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize review database: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) ViewedFiles(repoID, commitHash string) ([]string, error) {
	rows, err := s.db.Query(`SELECT file_key FROM viewed_files WHERE repo_id = ? AND commit_hash = ? ORDER BY file_key`, repoID, commitHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []string{}
	for rows.Next() {
		var file string
		if err := rows.Scan(&file); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

func (s *Store) SetViewed(repoID, commitHash, fileKey string, viewed bool) error {
	if fileKey == "" || len(fileKey) > 10_000 {
		return errors.New("invalid file key")
	}
	if viewed {
		_, err := s.db.Exec(`INSERT OR IGNORE INTO viewed_files(repo_id, commit_hash, file_key) VALUES (?, ?, ?)`, repoID, commitHash, fileKey)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM viewed_files WHERE repo_id = ? AND commit_hash = ? AND file_key = ?`, repoID, commitHash, fileKey)
	return err
}

func (s *Store) Settings() (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM app_settings WHERE id = 1`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "{}", nil
	}
	return value, err
}

func (s *Store) SaveSettings(value string) error {
	_, err := s.db.Exec(`INSERT INTO app_settings(id, value) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET value = excluded.value`, value)
	return err
}

func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT path, name, selected FROM projects ORDER BY selected DESC, name, path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []Project{}
	for rows.Next() {
		var project Project
		if err := rows.Scan(&project.Path, &project.Name, &project.Selected); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (s *Store) SelectProject(path, name string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE projects SET selected = 0 WHERE selected = 1`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO projects(path, name, selected, added_at) VALUES (?, ?, 1, ?)
		ON CONFLICT(path) DO UPDATE SET name = excluded.name, selected = 1`, path, name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Comments(repoID, commitHash, patchID string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.comments(repoID, commitHash, patchID)
}

func (s *Store) comments(repoID, commitHash, patchID string) (Result, error) {
	comments, err := s.query(repoID, "commit_hash", commitHash)
	if err != nil {
		return Result{}, err
	}
	if len(comments) > 0 || patchID == "" {
		revision, err := s.ensureRevision(repoID, patchID, comments)
		return Result{Comments: comments, Revision: revision}, err
	}
	comments, err = s.query(repoID, "patch_id", patchID)
	if err != nil {
		return Result{}, err
	}
	revision, err := s.ensureRevision(repoID, patchID, comments)
	return Result{Comments: comments, Recovered: len(comments) > 0, Revision: revision}, err
}

func (s *Store) query(repoID, column, value string) ([]Comment, error) {
	if column != "commit_hash" && column != "patch_id" {
		return nil, errors.New("invalid review lookup")
	}
	rows, err := s.db.Query(`SELECT id, commit_hash, patch_id, file_path, side, line,
		context, body, created_at, updated_at FROM comments WHERE repo_id = ? AND `+column+` = ? ORDER BY created_at`, repoID, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	comments := []Comment{}
	for rows.Next() {
		var comment Comment
		if err := rows.Scan(&comment.ID, &comment.CommitHash, &comment.PatchID,
			&comment.FilePath, &comment.Side, &comment.Line, &comment.Context,
			&comment.Body, &comment.CreatedAt, &comment.UpdatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, rows.Err()
}

func (s *Store) Save(repoID, commitHash, patchID, expectedRevision string, comments []Comment) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	newRevision, err := randomRevision()
	if err != nil {
		return "", err
	}
	result, err := tx.Exec(`UPDATE review_revisions SET revision = ?
		WHERE repo_id = ? AND patch_id = ? AND revision = ?`, newRevision, repoID, patchID, expectedRevision)
	if err != nil {
		return "", err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if changed != 1 {
		return "", ErrRevisionConflict
	}
	if _, err := tx.Exec("DELETE FROM comments WHERE repo_id = ? AND (commit_hash = ? OR patch_id = ?)", repoID, commitHash, patchID); err != nil {
		return "", err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, comment := range comments {
		if comment.ID == "" || comment.FilePath == "" || comment.Line < 1 || comment.Body == "" {
			return "", errors.New("comment is missing a required field")
		}
		if comment.Side != "old" && comment.Side != "new" {
			return "", errors.New("comment side must be old or new")
		}
		createdAt := comment.CreatedAt
		if createdAt == "" {
			createdAt = now
		}
		_, err := tx.Exec(`INSERT INTO comments
			(id, repo_id, commit_hash, patch_id, file_path, side, line, context, body, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET repo_id=excluded.repo_id, commit_hash=excluded.commit_hash,
			patch_id=excluded.patch_id, file_path=excluded.file_path, side=excluded.side,
			line=excluded.line, context=excluded.context, body=excluded.body, updated_at=excluded.updated_at`,
			comment.ID, repoID, commitHash, patchID, comment.FilePath, comment.Side,
			comment.Line, comment.Context, comment.Body, createdAt, now)
		if err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return newRevision, nil
}

func (s *Store) ensureRevision(repoID, patchID string, comments []Comment) (string, error) {
	initial := revision(comments)
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO review_revisions(repo_id, patch_id, revision) VALUES (?, ?, ?)`, repoID, patchID, initial); err != nil {
		return "", err
	}
	var current string
	if err := s.db.QueryRow(`SELECT revision FROM review_revisions WHERE repo_id = ? AND patch_id = ?`, repoID, patchID).Scan(&current); err != nil {
		return "", err
	}
	return current, nil
}

func randomRevision() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func revision(comments []Comment) string {
	value, _ := json.Marshal(comments)
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}
