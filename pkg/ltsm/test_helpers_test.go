package ltsm

import (
	"testing"
)

// Shared synthetic fixtures for package tests.
// initModule creates and initializes a Module with the embind Imports.
func initModule(t *testing.T) (*Module, *Imports) {
	t.Helper()
	imp := NewImports()
	mod := NewModule(imp)
	imp.SetModule(mod)

	// Phase 1: __wasm_call_ctors
	mod.fP()
	// Phase 2: embind type/class registrations
	mod.fT()

	return mod, imp
}
