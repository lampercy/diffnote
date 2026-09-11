package gitrepo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	commandTimeout = 15 * time.Second
	maxGitOutput   = 64 << 20
	maxGitError    = 1 << 20
)

var errOutputLimit = errors.New("Git output limit exceeded")

type Repository struct {
	Root          string `json:"root"`
	Name          string `json:"name"`
	ID            string `json:"-"`
	ContextID     string `json:"-"`
	CurrentBranch string `json:"currentBranch"`
}

type Branch struct {
	Name       string `json:"name"`
	Hash       string `json:"hash"`
	Current    bool   `json:"current"`
	Remote     bool   `json:"remote"`
	RemoteName string `json:"remoteName,omitempty"`
}

type Commit struct {
	Hash      string   `json:"hash"`
	ShortHash string   `json:"shortHash"`
	Parents   []string `json:"parents"`
	Author    string   `json:"author"`
	Date      string   `json:"date"`
	Subject   string   `json:"subject"`
}

type CommitDetail struct {
	Commit
	Body    string     `json:"body"`
	PatchID string     `json:"patchId"`
	Files   []FileDiff `json:"files"`
}

func Open(path string) (*Repository, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve repository path: %w", err)
	}

	root, err := runAt(absPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errors.New("path is not inside a Git repository")
	}
	root = strings.TrimSpace(root)

	commonDir, err := runAt(root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("resolve Git directory: %w", err)
	}

	branch, err := runAt(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		branch = "(detached)"
	}

	contextHash := sha256.Sum256([]byte(root))
	return &Repository{
		Root:          root,
		Name:          filepath.Base(root),
		ID:            strings.TrimSpace(commonDir),
		ContextID:     hex.EncodeToString(contextHash[:]),
		CurrentBranch: strings.TrimSpace(branch),
	}, nil
}

func (r *Repository) Branches() ([]Branch, error) {
	remoteOutput, err := runAt(r.Root, "remote")
	if err != nil {
		return nil, err
	}
	remoteNames := strings.Fields(remoteOutput)
	output, err := runAt(r.Root,
		"for-each-ref",
		"--sort=-committerdate",
		"--format=%(refname:short)%00%(refname)%00%(objectname)%00%(HEAD)",
		"refs/heads",
		"refs/remotes",
	)
	if err != nil {
		return nil, err
	}

	var branches []Branch
	for _, record := range strings.Split(strings.TrimSpace(output), "\n") {
		parts := strings.Split(record, "\x00")
		if len(parts) != 4 || strings.HasSuffix(parts[0], "/HEAD") {
			continue
		}
		remoteName := ""
		if strings.HasPrefix(parts[1], "refs/remotes/") {
			remoteRef := strings.TrimPrefix(parts[1], "refs/remotes/")
			remoteName = matchRemoteName(remoteRef, remoteNames)
			if remoteName == "" {
				remoteName = strings.SplitN(remoteRef, "/", 2)[0]
			}
		}
		branches = append(branches, Branch{
			Name:       parts[0],
			Hash:       parts[2],
			Current:    strings.TrimSpace(parts[3]) == "*",
			Remote:     strings.HasPrefix(parts[1], "refs/remotes/"),
			RemoteName: remoteName,
		})
	}
	return branches, nil
}

func matchRemoteName(ref string, remotes []string) string {
	match := ""
	for _, remote := range remotes {
		if strings.HasPrefix(ref, remote+"/") && len(remote) > len(match) {
			match = remote
		}
	}
	return match
}

func (r *Repository) Commits(ref string, limit int) ([]Commit, error) {
	return r.CommitPage(ref, limit, 0)
}

func (r *Repository) CommitPage(ref string, limit, offset int) ([]Commit, error) {
	return r.CommitPageSearch(ref, limit, offset, "")
}

func (r *Repository) CommitPageSearch(ref string, limit, offset int, search string) ([]Commit, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		return nil, errors.New("commit offset cannot be negative")
	}
	if ref == "" || ref == "(detached)" {
		ref = "HEAD"
	}
	if ref == "HEAD" {
		if _, err := runAt(r.Root, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
			return []Commit{}, nil
		}
	}

	args := []string{
		"log",
		"--topo-order",
		"--date=iso-strict",
		"--format=%H%x00%h%x00%P%x00%an%x00%aI%x00%s",
		"-n", strconv.Itoa(limit),
		"--skip", strconv.Itoa(offset),
	}
	if search != "" {
		args = append(args, "--regexp-ignore-case", "--fixed-strings", "--grep="+search)
	}
	args = append(args, "--end-of-options", ref)
	output, err := runAt(r.Root, args...)
	if err != nil {
		return nil, err
	}

	var commits []Commit
	for _, record := range strings.Split(strings.TrimSpace(output), "\n") {
		parts := strings.Split(record, "\x00")
		if len(parts) != 6 {
			continue
		}
		parents := []string{}
		if parts[2] != "" {
			parents = strings.Fields(parts[2])
		}
		commits = append(commits, Commit{
			Hash:      parts[0],
			ShortHash: parts[1],
			Parents:   parents,
			Author:    parts[3],
			Date:      parts[4],
			Subject:   parts[5],
		})
	}
	return commits, nil
}

func (r *Repository) CommitCount(ref string) (int, error) {
	return r.CommitCountSearch(ref, "")
}

func (r *Repository) CommitCountSearch(ref, search string) (int, error) {
	if ref == "" || ref == "(detached)" {
		ref = "HEAD"
	}
	if ref == "HEAD" {
		if _, err := runAt(r.Root, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
			return 0, nil
		}
	}
	args := []string{"rev-list", "--count"}
	if search != "" {
		args = append(args, "--regexp-ignore-case", "--fixed-strings", "--grep="+search)
	}
	args = append(args, "--end-of-options", ref)
	output, err := runAt(r.Root, args...)
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return 0, errors.New("unexpected Git commit count")
	}
	return count, nil
}

func (r *Repository) ResolveCommit(ref string) (string, error) {
	if ref == "" || ref == "(detached)" {
		ref = "HEAD"
	}
	output, err := runAt(r.Root, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		if ref == "HEAD" {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func (r *Repository) Commit(hash string) (*CommitDetail, error) {
	return r.CommitWithContext(hash, 3)
}

func (r *Repository) CommitWithContext(hash string, contextLines int) (*CommitDetail, error) {
	if !validHash(hash) {
		return nil, errors.New("invalid commit hash")
	}
	if contextLines < 0 || contextLines > 100 {
		return nil, errors.New("diff context must be between 0 and 100 lines")
	}
	metadata, err := runAt(r.Root,
		"show", "-s",
		"--date=iso-strict",
		"--format=%H%x00%h%x00%P%x00%an%x00%aI%x00%s%x00%b",
		"--end-of-options", hash,
	)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(strings.TrimSuffix(metadata, "\n"), "\x00", 7)
	if len(parts) != 7 {
		return nil, errors.New("unexpected Git commit metadata")
	}

	patchBytes, err := r.patch(hash, contextLines)
	if err != nil {
		return nil, err
	}
	files, err := ParseUnifiedDiff(string(patchBytes))
	if err != nil {
		return nil, err
	}
	identityPatch := patchBytes
	if contextLines != 3 {
		identityPatch, err = r.patch(hash, 3)
		if err != nil {
			return nil, err
		}
	}
	patchID := canonicalPatchID(identityPatch)

	parents := []string{}
	if parts[2] != "" {
		parents = strings.Fields(parts[2])
	}
	return &CommitDetail{
		Commit: Commit{
			Hash:      parts[0],
			ShortHash: parts[1],
			Parents:   parents,
			Author:    parts[3],
			Date:      parts[4],
			Subject:   parts[5],
		},
		Body:    strings.TrimSpace(parts[6]),
		PatchID: patchID,
		Files:   files,
	}, nil
}

func (r *Repository) PatchID(hash string) (string, error) {
	if !validHash(hash) {
		return "", errors.New("invalid commit hash")
	}
	patch, err := r.patch(hash, 3)
	if err != nil {
		return "", err
	}

	return canonicalPatchID(patch), nil
}

func (r *Repository) patch(hash string, contextLines int) ([]byte, error) {
	parents, err := runAt(r.Root, "rev-list", "--parents", "-n", "1", "--end-of-options", hash)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(parents)
	if len(fields) > 1 {
		return runBytesAt(r.Root,
			"-c", "core.quotePath=false",
			"diff", "--find-renames", "--no-color", "--no-ext-diff", "--no-textconv", "--binary", "--full-index", "--unified="+strconv.Itoa(contextLines),
			"--end-of-options", fields[1], hash,
		)
	}
	return runBytesAt(r.Root,
		"-c", "core.quotePath=false",
		"show", "--format=", "--find-renames", "--no-color", "--no-ext-diff", "--no-textconv", "--binary", "--full-index", "--unified="+strconv.Itoa(contextLines),
		"--end-of-options", hash,
	)
}

func canonicalPatchID(patch []byte) string {
	hash := sha256.New()
	for _, line := range bytes.Split(patch, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("index ")) {
			continue
		}
		if bytes.HasPrefix(line, []byte("@@ ")) {
			if marker := bytes.Index(line[3:], []byte(" @@")); marker >= 0 {
				line = append([]byte("@@"), line[3+marker+3:]...)
			}
		}
		hash.Write(line)
		hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func validHash(hash string) bool {
	if len(hash) < 4 || len(hash) > 64 {
		return false
	}
	for _, char := range hash {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func runAt(dir string, args ...string) (string, error) {
	output, err := runBytesAt(dir, args...)
	return string(output), err
}

func runBytesAt(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	output := limitedBuffer{limit: maxGitOutput}
	errorOutput := limitedBuffer{limit: maxGitError}
	cmd.Stdout = &output
	cmd.Stderr = &errorOutput
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, errors.New("Git command timed out")
	}
	if output.exceeded {
		return nil, fmt.Errorf("git %s output exceeds %d MiB", args[0], maxGitOutput>>20)
	}
	if errorOutput.exceeded {
		return nil, fmt.Errorf("git %s error output exceeds %d MiB", args[0], maxGitError>>20)
	}
	if err != nil {
		message := strings.TrimSpace(errorOutput.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", args[0], message)
	}
	return output.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	remaining := b.limit - b.Len()
	if len(value) <= remaining {
		return b.Buffer.Write(value)
	}
	b.exceeded = true
	if remaining > 0 {
		_, _ = b.Buffer.Write(value[:remaining])
	}
	return remaining, errOutputLimit
}
