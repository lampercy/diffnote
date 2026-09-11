package gitrepo

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type FileDiff struct {
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
	Status  string `json:"status"`
	Binary  bool   `json:"binary"`
	Hunks   []Hunk `json:"hunks"`
}

type Hunk struct {
	Header string     `json:"header"`
	Lines  []DiffLine `json:"lines"`
}

type DiffLine struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
	OldLine *int   `json:"oldLine"`
	NewLine *int   `json:"newLine"`
}

var hunkPattern = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func ParseUnifiedDiff(input string) ([]FileDiff, error) {
	var files []FileDiff
	var file *FileDiff
	var hunk *Hunk
	oldLine, newLine := 0, 0

	scanner := bufio.NewScanner(strings.NewReader(input))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			files = append(files, FileDiff{Status: "modified", Hunks: []Hunk{}})
			file = &files[len(files)-1]
			hunk = nil
			parseDiffHeader(line, file)
		case file == nil:
			continue
		case strings.HasPrefix(line, "new file mode "):
			file.Status = "added"
		case strings.HasPrefix(line, "deleted file mode "):
			file.Status = "deleted"
		case strings.HasPrefix(line, "rename from "):
			file.Status = "renamed"
			file.OldPath = decodeGitPath(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			file.NewPath = decodeGitPath(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
			file.Binary = true
		case strings.HasPrefix(line, "--- "):
			path := parseMarkerPath(strings.TrimPrefix(line, "--- "))
			if path != "/dev/null" {
				file.OldPath = path
			}
		case strings.HasPrefix(line, "+++ "):
			path := parseMarkerPath(strings.TrimPrefix(line, "+++ "))
			if path != "/dev/null" {
				file.NewPath = path
			}
		case strings.HasPrefix(line, "@@ "):
			match := hunkPattern.FindStringSubmatch(line)
			if len(match) != 3 {
				return nil, fmt.Errorf("parse hunk header %q", line)
			}
			oldLine, _ = strconv.Atoi(match[1])
			newLine, _ = strconv.Atoi(match[2])
			file.Hunks = append(file.Hunks, Hunk{Header: line, Lines: []DiffLine{}})
			hunk = &file.Hunks[len(file.Hunks)-1]
		case hunk != nil:
			diffLine := DiffLine{Content: line}
			switch {
			case strings.HasPrefix(line, "+"):
				diffLine.Kind = "add"
				diffLine.NewLine = intPointer(newLine)
				newLine++
			case strings.HasPrefix(line, "-"):
				diffLine.Kind = "delete"
				diffLine.OldLine = intPointer(oldLine)
				oldLine++
			case strings.HasPrefix(line, " "):
				diffLine.Kind = "context"
				diffLine.OldLine = intPointer(oldLine)
				diffLine.NewLine = intPointer(newLine)
				oldLine++
				newLine++
			default:
				diffLine.Kind = "meta"
			}
			hunk.Lines = append(hunk.Lines, diffLine)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan Git diff: %w", err)
	}
	return files, nil
}

func parseDiffHeader(line string, file *FileDiff) {
	value := strings.TrimPrefix(line, "diff --git ")
	if strings.HasPrefix(value, "\"") {
		fields := quotedFields(value)
		if len(fields) == 2 {
			file.OldPath = strings.TrimPrefix(fields[0], "a/")
			file.NewPath = strings.TrimPrefix(fields[1], "b/")
		}
		return
	}
	separator := strings.LastIndex(value, " b/")
	if separator < 0 {
		return
	}
	file.OldPath = strings.TrimPrefix(value[:separator], "a/")
	file.NewPath = strings.TrimPrefix(value[separator+1:], "b/")
}

func parseMarkerPath(value string) string {
	value = strings.SplitN(value, "\t", 2)[0]
	value = decodeGitPath(value)
	value = strings.TrimPrefix(value, "a/")
	value = strings.TrimPrefix(value, "b/")
	return value
}

func decodeGitPath(value string) string {
	if strings.HasPrefix(value, "\"") {
		if decoded, err := strconv.Unquote(value); err == nil {
			return decoded
		}
	}
	return value
}

func quotedFields(value string) []string {
	fields := []string{}
	for len(value) > 0 {
		value = strings.TrimLeft(value, " ")
		if value == "" {
			break
		}
		if value[0] != '"' {
			fields = append(fields, strings.Fields(value)...)
			break
		}
		end, escaped := 1, false
		for ; end < len(value); end++ {
			if value[end] == '"' && !escaped {
				end++
				break
			}
			if value[end] == '\\' && !escaped {
				escaped = true
			} else {
				escaped = false
			}
		}
		fields = append(fields, decodeGitPath(value[:end]))
		value = value[end:]
	}
	return fields
}

func intPointer(value int) *int {
	return &value
}
