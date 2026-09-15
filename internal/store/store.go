// Package store owns envkit's on-disk project files and their local Git audit log.
package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Store is the local envkit repository for one selected project.
type Store struct {
	Home    string
	Project string
	CWD     string
}

// New resolves the current project and creates a store handle. It does not modify disk.
func New(cwd string) (*Store, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	home := os.Getenv("ENVKIT_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(userHome, ".envkit")
	}
	project, err := ResolveProject(cwd)
	if err != nil {
		return nil, err
	}
	return &Store{Home: home, Project: project, CWD: cwd}, nil
}

// NewForProject opens a known project, primarily for the TUI and tests.
func NewForProject(home, project string) *Store {
	return &Store{Home: home, Project: project}
}

// ResolveProject applies envkit's project precedence rules.
func ResolveProject(cwd string) (string, error) {
	if project := os.Getenv("ENVKIT_PROJECT"); project != "" {
		return project, nil
	}
	if configured := gitOutput(cwd, "config", "--get", "envkit.project"); configured != "" {
		return configured, nil
	}
	if remote := gitOutput(cwd, "config", "--get", "remote.origin.url"); remote != "" {
		if slug, ok := Slug(remote); ok {
			return slug, nil
		}
	}
	if top := gitOutput(cwd, "rev-parse", "--show-toplevel"); top != "" {
		return filepath.Base(top), nil
	}
	return filepath.Base(cwd), nil
}

// Slug extracts owner__repo from HTTPS, ssh URL, scp syntax, and host aliases.
func Slug(remote string) (string, bool) {
	remote = strings.TrimSuffix(strings.TrimSpace(remote), ".git")
	remote = strings.TrimSuffix(remote, "/")
	lastSlash := strings.LastIndex(remote, "/")
	lastColon := strings.LastIndex(remote, ":")
	cut := lastSlash
	if lastColon > cut {
		cut = lastColon
	}
	if cut < 0 || cut == len(remote)-1 {
		return "", false
	}
	repo := remote[cut+1:]
	parent := remote[:cut]
	ownerCut := strings.LastIndexAny(parent, "/:")
	if ownerCut < 0 || ownerCut == len(parent)-1 || repo == "" {
		return "", false
	}
	return parent[ownerCut+1:] + "__" + repo, true
}

func gitOutput(cwd string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (s *Store) ProjectDir() string { return filepath.Join(s.Home, s.Project) }
func (s *Store) Path() string       { return filepath.Join(s.ProjectDir(), ".env") }

// Ensure creates the private store home, its Git repository, and this project's file.
func (s *Store) Ensure() error {
	if err := os.MkdirAll(s.Home, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.Home, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(s.Home, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := s.git("init", "-q"); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(s.ProjectDir(), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(s.Path(), os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}

func (s *Store) git(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.Home
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// ValidateKey rejects strings that are not legal shell environment names.
func ValidateKey(key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("envkit: invalid key: %s", key)
	}
	return nil
}

// Document preserves every physical line so unrelated content is round-tripped verbatim.
type Document struct {
	Lines []string
}

func Parse(data []byte) Document {
	text := string(data)
	if text == "" {
		return Document{}
	}
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return Document{Lines: lines}
}

func (d Document) Bytes() []byte {
	if len(d.Lines) == 0 {
		return nil
	}
	return []byte(strings.Join(d.Lines, "\n") + "\n")
}

func (d Document) Value(key string) (string, bool) {
	prefix := key + "="
	for _, line := range d.Lines {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix), true
		}
	}
	return "", false
}

func keyAt(line string) (string, bool) {
	at := strings.IndexByte(line, '=')
	if at < 1 {
		return "", false
	}
	key := line[:at]
	return key, keyPattern.MatchString(key)
}

func commentStart(lines []string, index int) int {
	start := index
	for start > 0 && strings.HasPrefix(lines[start-1], "#") {
		start--
	}
	return start
}

// Set replaces a key in place. Existing adjacent comments remain unless comment is non-nil.
func (d *Document) Set(key, value string, comment *string) {
	line := key + "=" + value
	for i, existing := range d.Lines {
		if found, ok := keyAt(existing); ok && found == key {
			if comment != nil {
				start := commentStart(d.Lines, i)
				replacement := []string{"# " + *comment, line}
				d.Lines = append(append(d.Lines[:start:start], replacement...), d.Lines[i+1:]...)
			} else {
				d.Lines[i] = line
			}
			return
		}
	}
	if comment != nil {
		d.Lines = append(d.Lines, "# "+*comment)
	}
	d.Lines = append(d.Lines, line)
}

// Unset removes a key and all directly preceding comment lines.
func (d *Document) Unset(key string) bool {
	for i, existing := range d.Lines {
		if found, ok := keyAt(existing); ok && found == key {
			start := commentStart(d.Lines, i)
			d.Lines = append(d.Lines[:start], d.Lines[i+1:]...)
			return true
		}
	}
	return false
}

func (d Document) Entries() []Entry {
	var entries []Entry
	var comments []string
	for _, line := range d.Lines {
		if strings.HasPrefix(line, "#") {
			comment := strings.TrimPrefix(line, "#")
			comments = append(comments, strings.TrimPrefix(comment, " "))
			continue
		}
		if key, ok := keyAt(line); ok {
			value := strings.TrimPrefix(line, key+"=")
			entries = append(entries, Entry{Key: key, Value: value, Comment: strings.Join(comments, "\n")})
		}
		comments = nil
	}
	return entries
}

type Entry struct {
	Key     string
	Value   string
	Comment string
}

func (s *Store) Read() (Document, error) {
	if err := s.Ensure(); err != nil {
		return Document{}, err
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		return Document{}, err
	}
	return Parse(data), nil
}

func (s *Store) write(d Document) error {
	return os.WriteFile(s.Path(), d.Bytes(), 0o600)
}

func (s *Store) Set(key, value string, comment *string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	d, err := s.Read()
	if err != nil {
		return err
	}
	d.Set(key, value, comment)
	if err := s.write(d); err != nil {
		return err
	}
	return s.Commit(fmt.Sprintf("%s: set %s", s.Project, key))
}

func (s *Store) Comment(key, text string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	d, err := s.Read()
	if err != nil {
		return err
	}
	value, ok := d.Value(key)
	if !ok {
		return fmt.Errorf("envkit: no such key: %s", key)
	}
	d.Set(key, value, &text)
	if err := s.write(d); err != nil {
		return err
	}
	return s.Commit(fmt.Sprintf("%s: comment %s", s.Project, key))
}

func (s *Store) Unset(key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	d, err := s.Read()
	if err != nil {
		return err
	}
	d.Unset(key)
	if err := s.write(d); err != nil {
		return err
	}
	return s.Commit(fmt.Sprintf("%s: unset %s", s.Project, key))
}

func (s *Store) Get(key string) (string, bool, error) {
	if err := ValidateKey(key); err != nil {
		return "", false, err
	}
	d, err := s.Read()
	if err != nil {
		return "", false, err
	}
	value, ok := d.Value(key)
	return value, ok, nil
}

func (s *Store) Entries() ([]Entry, error) {
	d, err := s.Read()
	if err != nil {
		return nil, err
	}
	return d.Entries(), nil
}

// Commit stages this project's env file and only creates a commit when content changed.
func (s *Store) Commit(message string) error {
	if _, err := s.git("add", filepath.ToSlash(filepath.Join(s.Project, ".env"))); err != nil {
		return err
	}
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	cmd.Dir = s.Home
	if err := cmd.Run(); err == nil {
		return nil
	}
	_, err := s.git("commit", "-q", "-m", message)
	return err
}

func (s *Store) Log(key string) (string, error) {
	if err := s.Ensure(); err != nil {
		return "", err
	}
	args := []string{"log", "--oneline"}
	if key != "" {
		args = []string{"log", "-p", "-S", key}
	}
	args = append(args, "--", s.Project)
	out, err := s.git(args...)
	return string(out), err
}

func (s *Store) KeyHistory(key, format string) (string, error) {
	if err := s.Ensure(); err != nil {
		return "", err
	}
	out, err := s.git("log", "--format="+format, "-S", key, "--", s.Project)
	return string(out), err
}

func (s *Store) Diff() (string, error) {
	if err := s.Ensure(); err != nil {
		return "", err
	}
	out, err := s.git("diff")
	return string(out), err
}

func (s *Store) Projects() ([]string, error) {
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.Home)
	if err != nil {
		return nil, err
	}
	var projects []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != ".git" {
			projects = append(projects, entry.Name())
		}
	}
	sort.Strings(projects)
	return projects, nil
}

// MergeEnv overlays values from the document on an environment list.
func MergeEnv(base []string, entries []Entry) []string {
	values := make(map[string]string, len(base)+len(entries))
	order := make([]string, 0, len(base)+len(entries))
	for _, item := range base {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = value
	}
	for _, entry := range entries {
		if _, exists := values[entry.Key]; !exists {
			order = append(order, entry.Key)
		}
		values[entry.Key] = entry.Value
	}
	merged := make([]string, 0, len(order))
	for _, key := range order {
		merged = append(merged, key+"="+values[key])
	}
	return merged
}

// CopyTo writes the raw document to w. It is useful to CLI callers that need no parsing.
func (s *Store) CopyTo(w io.Writer) error {
	d, err := s.Read()
	if err != nil {
		return err
	}
	_, err = w.Write(d.Bytes())
	return err
}
