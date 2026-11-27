// Package vault lets another Go module read and write one envkit project.
// It uses envkit's own store, so its lock, file modes and history apply.
package vault

import (
	"os"
	"path/filepath"

	"github.com/NarayanaSabari/envkit/internal/store"
)

// Vault is one project of an envkit home.
type Vault struct{ s *store.Store }

// Open opens one project of the vault at home. An empty home is envkit's
// own: ENVKIT_HOME, or ~/.envkit. A project envkit refuses is an error.
func Open(home, project string) (*Vault, error) {
	if home == "" {
		home = os.Getenv("ENVKIT_HOME")
	}
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(h, ".envkit")
	}
	s := store.NewForProject(home, project)
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	return &Vault{s}, nil
}

// Get returns a key's value, and whether the key is set.
func (v *Vault) Get(key string) (string, bool, error) { return v.s.Get(key) }

// Set writes a key with its comment and records the change in the vault's
// history. An error never holds the value.
func (v *Vault) Set(key, value, comment string) error { return v.s.Set(key, value, &comment) }
