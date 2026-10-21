// Package store owns envkit's on-disk project files and their local Git audit log.
package store

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Store is the local envkit repository for one selected project.
type Store struct {
	Home      string
	Project   string
	directory string
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
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	project, err := ResolveProject(cwd)
	if err != nil {
		return nil, err
	}
	directory := cwd
	if top := gitOutput(cwd, "rev-parse", "--show-toplevel"); top != "" {
		directory = top
	}
	return &Store{Home: home, Project: project, directory: directory}, nil
}

// NewForProject opens a known project, primarily for the TUI and tests.
// Filesystem entry points validate the project, preserving this constructor's signature.
func NewForProject(home, project string) *Store {
	return &Store{Home: home, Project: project}
}

// ResolveProject applies envkit's project precedence rules.
func ResolveProject(cwd string) (string, error) {
	project := resolveProject(cwd)
	if err := validateProject(project); err != nil {
		return "", err
	}
	return project, nil
}

func validateProject(project string) error {
	if project == "" || project == "." || project == ".." || strings.EqualFold(project, ".git") || strings.ContainsAny(project, "/\\\x00") {
		return fmt.Errorf("envkit: invalid project: %q", project)
	}
	return nil
}

func resolveProject(cwd string) string {
	if project := os.Getenv("ENVKIT_PROJECT"); project != "" {
		return project
	}
	if configured := gitOutput(cwd, "config", "--get", "envkit.project"); configured != "" {
		return configured
	}
	if remote := gitOutput(cwd, "config", "--get", "remote.origin.url"); remote != "" {
		if slug, ok := Slug(remote); ok {
			return slug
		}
	}
	if top := gitOutput(cwd, "rev-parse", "--show-toplevel"); top != "" {
		return filepath.Base(top)
	}
	return filepath.Base(cwd)
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
	return s.withLock(func() error { return nil })
}

// withLock serializes all envkit processes from load through audit commit.
// Locked helpers never call public methods, which would acquire this lock again.
func (s *Store) withLock(fn func() error) error {
	if err := validateProject(s.Project); err != nil {
		return err
	}
	return s.withVaultLock(func() error {
		if err := s.ensure(); err != nil {
			return err
		}
		return fn()
	})
}

func (s *Store) withVaultLock(fn func() error) error {
	return s.withHomeLock(func() error {
		if err := s.ensureRepository(); err != nil {
			return err
		}
		return fn()
	})
}

// withHomeLock also serves metadata reads, which need no Git process.
func (s *Store) withHomeLock(fn func() error) error {
	if err := os.MkdirAll(s.Home, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.Home, 0o700); err != nil {
		return err
	}
	fd, err := unix.Open(filepath.Join(s.Home, ".envkit.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	lock := os.NewFile(uintptr(fd), "envkit lock")
	defer lock.Close() // Closing releases flock, including on error paths.
	for {
		err = unix.Flock(fd, unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		return err
	}
	return fn()
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("envkit: symlink not allowed: %s", path)
	}
	return nil
}

func (s *Store) ensureRepository() error {
	gitDir := filepath.Join(s.Home, ".git")
	if err := rejectSymlink(gitDir); err != nil {
		return err
	}
	if _, err := os.Stat(gitDir); errors.Is(err, os.ErrNotExist) {
		if _, err := s.git("init", "-q"); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

func (s *Store) ensure() error {
	if err := rejectSymlink(s.ProjectDir()); err != nil {
		return err
	}
	if err := os.MkdirAll(s.ProjectDir(), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.ProjectDir(), 0o700); err != nil {
		return err
	}
	if err := rejectSymlink(s.Path()); err != nil {
		return err
	}
	if info, err := os.Lstat(s.Path()); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("envkit: env file must be regular: %s", s.Path())
	}
	file, err := os.OpenFile(s.Path(), os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return s.recordDirectory()
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
				replacement := append(commentLines(*comment), line)
				d.Lines = append(append(d.Lines[:start:start], replacement...), d.Lines[i+1:]...)
			} else {
				d.Lines[i] = line
			}
			return
		}
	}
	if comment != nil {
		d.Lines = append(d.Lines, commentLines(*comment)...)
	}
	d.Lines = append(d.Lines, line)
}

func commentLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(strings.ReplaceAll(text, "\r", "\n"), "\n")
	for i := range lines {
		lines[i] = "# " + lines[i]
	}
	return lines
}

func validateValue(value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("envkit: values cannot contain CR, LF, or NUL")
	}
	return nil
}

func (d Document) validate() error {
	seen := make(map[string]bool)
	for _, line := range d.Lines {
		if strings.ContainsRune(line, '\x00') {
			return errors.New("envkit: document cannot contain NUL")
		}
		if key, ok := keyAt(line); ok {
			if seen[key] {
				return fmt.Errorf("envkit: duplicate key: %s", key)
			}
			seen[key] = true
			if err := validateValue(strings.TrimPrefix(line, key+"=")); err != nil {
				return fmt.Errorf("envkit: key %s: %w (CRLF documents are unsupported)", key, err)
			}
		}
	}
	return nil
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
	var d Document
	err := s.withLock(func() error {
		var err error
		d, err = s.read()
		return err
	})
	return d, err
}

func (s *Store) read() (Document, error) {
	data, err := os.ReadFile(s.Path())
	if err != nil {
		return Document{}, err
	}
	d := Parse(data)
	return d, d.validate()
}

// tempFile closes a private same-directory file before it can replace .env.
func (s *Store) tempFile(data []byte) (name string, err error) {
	file, err := os.CreateTemp(s.ProjectDir(), ".envkit-edit-*")
	if err != nil {
		return "", err
	}
	name = file.Name()
	defer func() {
		if err != nil {
			os.Remove(name)
		}
	}()
	if err = file.Chmod(0o600); err != nil {
		file.Close()
		return name, err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return name, err
	}
	err = file.Close()
	return name, err
}

func (s *Store) write(d Document) error {
	name, err := s.tempFile(d.Bytes())
	if err != nil {
		return err
	}
	defer os.Remove(name)
	return os.Rename(name, s.Path())
}

func (s *Store) update(message string, change func(*Document) error) error {
	return s.withLock(func() error {
		d, err := s.read()
		if err != nil {
			return err
		}
		if err := change(&d); err != nil {
			return err
		}
		if err := d.validate(); err != nil {
			return err
		}
		if err := s.write(d); err != nil {
			return err
		}
		return s.commit(message)
	})
}

func (s *Store) Set(key, value string, comment *string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := validateValue(value); err != nil {
		return err
	}
	return s.update(fmt.Sprintf("%s: set %s", s.Project, key), func(d *Document) error {
		d.Set(key, value, comment)
		return nil
	})
}

func (s *Store) Comment(key, text string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	return s.update(fmt.Sprintf("%s: comment %s", s.Project, key), func(d *Document) error {
		value, ok := d.Value(key)
		if !ok {
			return fmt.Errorf("envkit: no such key: %s", key)
		}
		d.Set(key, value, &text)
		return nil
	})
}

func (s *Store) Unset(key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	return s.update(fmt.Sprintf("%s: unset %s", s.Project, key), func(d *Document) error {
		d.Unset(key)
		return nil
	})
}

// Edit holds the vault lock while editing a private temporary copy. Invalid or
// failed edits leave a recovery file, never replacing the project's document.
func (s *Store) Edit(editor []string) error {
	if len(editor) == 0 || editor[0] == "" {
		return errors.New("envkit: editor command is empty")
	}
	return s.withLock(func() error {
		data, err := os.ReadFile(s.Path())
		if err != nil {
			return err
		}
		if err := Parse(data).validate(); err != nil {
			return err
		}
		name, err := s.tempFile(data)
		if err != nil {
			return err
		}
		args := append(append([]string(nil), editor[1:]...), name)
		cmd := exec.Command(editor[0], args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		editorErr := cmd.Run()
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("envkit: edited file must be regular: %s", name)
		}
		file, err := os.OpenFile(name, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		if err := file.Chmod(0o600); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		if editorErr != nil {
			return fmt.Errorf("envkit: editor failed; recover edits at %s: %w", name, editorErr)
		}
		data, err = os.ReadFile(name)
		if err != nil {
			return fmt.Errorf("envkit: cannot read edits at %s: %w", name, err)
		}
		if err := Parse(data).validate(); err != nil {
			return fmt.Errorf("envkit: invalid edits; recover edits at %s: %w", name, err)
		}
		if err := os.Rename(name, s.Path()); err != nil {
			return fmt.Errorf("envkit: cannot install edits; recover edits at %s: %w", name, err)
		}
		return s.commit(fmt.Sprintf("%s: edit", s.Project))
	})
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

// Commit audits only this project's env file, preserving unrelated staged changes.
func (s *Store) Commit(message string) error {
	return s.withLock(func() error {
		if _, err := s.read(); err != nil {
			return err
		}
		return s.commit(message)
	})
}

func (s *Store) pathspec() string {
	return ":(literal)" + filepath.ToSlash(filepath.Join(s.Project, ".env"))
}

func (s *Store) commit(message string) error {
	return s.commitPaths(message, s.pathspec())
}

func (s *Store) commitPaths(message string, paths ...string) error {
	var stage []string
	for _, path := range paths {
		_, err := os.Lstat(filepath.Join(s.Home, filepath.FromSlash(strings.TrimPrefix(path, ":(literal)"))))
		if errors.Is(err, os.ErrNotExist) {
			// Already-staged deletions are in HEAD but no longer in the index.
			out, err := s.git("ls-files", "--", path)
			if err != nil {
				return err
			}
			if len(out) == 0 {
				continue
			}
		} else if err != nil {
			return err
		}
		stage = append(stage, path)
	}
	if len(stage) != 0 {
		if _, err := s.git(append([]string{"add", "-A", "--"}, stage...)...); err != nil {
			return err
		}
	}
	if _, err := s.git(append([]string{"diff", "--cached", "--quiet", "--"}, paths...)...); err == nil {
		return nil
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return err
		}
	}
	_, err := s.git(append([]string{"commit", "--only", "-q", "-m", message, "--"}, paths...)...)
	return err
}

func (s *Store) log(args ...string) (string, error) {
	var out []byte
	err := s.withLock(func() error {
		var err error
		out, err = s.git(append(args, "--follow", "--", s.pathspec())...)
		return err
	})
	return string(out), err
}

func (s *Store) Log(key string) (string, error) {
	if key == "" {
		return s.log("log", "--oneline")
	}
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	return s.log("log", "-p", "-G", "^"+key+"=")
}

func (s *Store) KeyHistory(key, format string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	return s.log("log", "--format="+format, "-G", "^"+key+"=")
}

// KeyHistories reads every key's history in one Git scan. Format must produce
// one metadata line per commit; changed values are not returned to callers.
func (s *Store) KeyHistories(format string) (map[string][]string, error) {
	text, err := s.log("log", "--format=%x00"+format, "-p", "--unified=0", "--no-color", "--no-ext-diff", "--no-textconv")
	if err != nil {
		return nil, err
	}
	histories := make(map[string][]string)
	for _, commit := range strings.Split(text, "\x00")[1:] {
		metadata, patch, _ := strings.Cut(commit, "\n")
		seen := make(map[string]bool)
		for line := range strings.SplitSeq(patch, "\n") {
			if len(line) == 0 || (line[0] != '+' && line[0] != '-') {
				continue
			}
			if key, ok := keyAt(line[1:]); ok && !seen[key] {
				histories[key] = append(histories[key], metadata)
				seen[key] = true
			}
		}
	}
	return histories, nil
}

func (s *Store) Diff() (string, error) {
	var out []byte
	err := s.withLock(func() error {
		var err error
		out, err = s.git("diff")
		return err
	})
	return string(out), err
}

func (s *Store) Projects() ([]string, error) {
	if err := validateProject(s.Project); err != nil {
		return nil, err
	}
	var projects []string
	err := s.withVaultLock(func() error {
		entries, err := os.ReadDir(s.Home)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() && validateProject(entry.Name()) == nil {
				projects = append(projects, entry.Name())
			}
		}
		return nil
	})
	sort.Strings(projects)
	return projects, err
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
