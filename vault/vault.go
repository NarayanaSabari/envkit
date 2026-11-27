// Package vault lets another Go module read and write one envkit project.
package vault

import "errors"

// Vault is one project of an envkit home.
type Vault struct{}

// Open opens one project of the vault at home.
func Open(home, project string) (*Vault, error) { return nil, errors.New("not built") }

// Get returns a key's value, and whether the key is set.
func (v *Vault) Get(key string) (string, bool, error) { return "", false, errors.New("not built") }

// Set writes a key with its comment.
func (v *Vault) Set(key, value, comment string) error { return errors.New("not built") }
