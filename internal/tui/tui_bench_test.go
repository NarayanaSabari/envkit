package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/NarayanaSabari/envkit/internal/store"
)

func BenchmarkTUI100Keys(b *testing.B) {
	b.Setenv("GIT_AUTHOR_NAME", "envkit benchmark")
	b.Setenv("GIT_AUTHOR_EMAIL", "benchmark@example.invalid")
	b.Setenv("GIT_COMMITTER_NAME", "envkit benchmark")
	b.Setenv("GIT_COMMITTER_EMAIL", "benchmark@example.invalid")
	s := store.NewForProject(b.TempDir(), "project")
	if err := s.Ensure(); err != nil {
		b.Fatal(err)
	}
	var data strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&data, "KEY_%03d=secret\n", i)
	}
	if err := os.WriteFile(s.Path(), []byte(data.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	if err := s.Commit("initial keys"); err != nil {
		b.Fatal(err)
	}
	model := New(s)
	b.Run("Load", func(b *testing.B) {
		for b.Loop() {
			model.loadKeys()
		}
	})
	b.Run("View", func(b *testing.B) {
		for b.Loop() {
			model.View()
		}
	})
}
