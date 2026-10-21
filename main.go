package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/NarayanaSabari/envkit/internal/store"
	"github.com/NarayanaSabari/envkit/internal/tui"
)

var version = "dev"

type cliError struct {
	code int
	err  error
}

func (e *cliError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: envkit <verb> [arguments]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "init | path | project | version | set KEY[=VALUE] [VALUE] [-m COMMENT] | unset KEY | comment KEY TEXT")
	fmt.Fprintln(w, "ls | get KEY | run [--] COMMAND... | export | log [KEY] | diff | edit | list | tui")
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		var cli *cliError
		if errors.As(err, &cli) {
			if cli.err != nil {
				fmt.Fprintln(os.Stderr, cli.err)
			}
			os.Exit(cli.code)
		}
		fmt.Fprintln(os.Stderr, "envkit:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	verb := "help"
	if len(args) > 0 {
		verb, args = args[0], args[1:]
	}
	if verb == "help" || verb == "-h" || verb == "--help" {
		usage(os.Stdout)
		return nil
	}
	if verb == "version" {
		if len(args) != 0 {
			usage(os.Stderr)
			return &cliError{code: 2}
		}
		fmt.Fprintln(os.Stdout, version)
		return nil
	}
	s, err := store.New("")
	if err != nil {
		return err
	}
	requireNoArgs := func() error {
		if len(args) != 0 {
			usage(os.Stderr)
			return &cliError{code: 2}
		}
		return nil
	}
	switch verb {
	case "init", "path":
		if err := requireNoArgs(); err != nil {
			return err
		}
		if err := s.Ensure(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, s.Path())
	case "project":
		if err := requireNoArgs(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, s.Project)
	case "set":
		return setCommand(s, args)
	case "unset":
		if len(args) != 1 {
			usage(os.Stderr)
			return &cliError{code: 2}
		}
		if err := s.Unset(args[0]); err != nil {
			return exitForKey(err)
		}
	case "comment":
		if len(args) != 2 {
			usage(os.Stderr)
			return &cliError{code: 2}
		}
		if err := s.Comment(args[0], args[1]); err != nil {
			return exitForKey(err)
		}
	case "ls":
		if err := requireNoArgs(); err != nil {
			return err
		}
		entries, err := s.Entries()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Comment == "" {
				fmt.Fprintln(os.Stdout, entry.Key)
			} else {
				fmt.Fprintf(os.Stdout, "%s  # %s\n", entry.Key, strings.ReplaceAll(entry.Comment, "\n", " "))
			}
		}
	case "get":
		if len(args) != 1 {
			usage(os.Stderr)
			return &cliError{code: 2}
		}
		value, _, err := s.Get(args[0])
		if err != nil {
			return exitForKey(err)
		}
		fmt.Fprintln(os.Stdout, value)
	case "run":
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) == 0 {
			usage(os.Stderr)
			return &cliError{code: 2}
		}
		entries, err := s.Entries()
		if err != nil {
			return err
		}
		path, err := exec.LookPath(args[0])
		if err != nil {
			return err
		}
		return syscall.Exec(path, args, store.MergeEnv(os.Environ(), entries))
	case "export":
		if err := requireNoArgs(); err != nil {
			return err
		}
		entries, err := s.Entries()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			fmt.Fprintf(os.Stdout, "export %s='%s'\n", entry.Key, strings.ReplaceAll(entry.Value, "'", "'\"'\"'"))
		}
	case "log":
		if len(args) > 1 {
			usage(os.Stderr)
			return &cliError{code: 2}
		}
		key := ""
		if len(args) == 1 {
			key = args[0]
		}
		out, err := s.Log(key)
		if err != nil {
			return err
		}
		fmt.Fprint(os.Stdout, out)
	case "diff":
		if err := requireNoArgs(); err != nil {
			return err
		}
		out, err := s.Diff()
		if err != nil {
			return err
		}
		fmt.Fprint(os.Stdout, out)
	case "edit":
		if err := requireNoArgs(); err != nil {
			return err
		}
		editor := strings.Fields(os.Getenv("EDITOR"))
		if len(editor) == 0 {
			editor = []string{"vi"}
		}
		return s.Edit(editor)
	case "list":
		if err := requireNoArgs(); err != nil {
			return err
		}
		projects, err := s.Projects()
		if err != nil {
			return err
		}
		for _, project := range projects {
			fmt.Fprintln(os.Stdout, project)
		}
	case "tui":
		if err := requireNoArgs(); err != nil {
			return err
		}
		return tui.Run(s)
	default:
		usage(os.Stderr)
		return &cliError{code: 2, err: fmt.Errorf("envkit: unknown verb: %s", verb)}
	}
	return nil
}

func setCommand(s *store.Store, args []string) error {
	if len(args) == 0 {
		usage(os.Stderr)
		return &cliError{code: 2}
	}
	var comment *string
	var positional []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-m" {
			if i+1 >= len(args) {
				return &cliError{code: 2, err: errors.New("envkit: -m needs a comment")}
			}
			comment = &args[i+1]
			i++
			continue
		}
		positional = append(positional, args[i])
	}
	if len(positional) == 0 {
		usage(os.Stderr)
		return &cliError{code: 2}
	}
	key, value, inline := strings.Cut(positional[0], "=")
	if err := store.ValidateKey(key); err != nil {
		return exitForKey(err)
	}
	switch {
	case len(positional) > 2, inline && len(positional) == 2:
		return &cliError{code: 2, err: errors.New("envkit: set takes one value: KEY=VALUE or KEY VALUE")}
	case len(positional) == 2:
		value, inline = positional[1], true
	}
	if !inline {
		var err error
		if value, err = readValue(key); err != nil {
			return err
		}
	}
	return s.Set(key, value, comment)
}

func exitForKey(err error) error {
	if strings.HasPrefix(err.Error(), "envkit: invalid key:") {
		return &cliError{code: 2, err: err}
	}
	return err
}

func readValue(key string) (value string, err error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		data, err := io.ReadAll(os.Stdin)
		return strings.TrimRight(string(data), "\n"), err
	}
	fmt.Fprintf(os.Stderr, "value for %s: ", key)
	state := exec.Command("stty", "-g")
	state.Stdin = os.Stdin
	saved, err := state.Output()
	if err != nil {
		return "", err
	}
	defer func() {
		on := exec.Command("stty", strings.TrimSpace(string(saved)))
		on.Stdin = os.Stdin
		err = errors.Join(err, on.Run())
		fmt.Fprintln(os.Stderr)
	}()
	off := exec.Command("stty", "-echo")
	off.Stdin = os.Stdin
	if err := off.Run(); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return strings.TrimRight(line, "\r\n"), err
}
