//go:build !windows

package cli

import (
	"context"
	"os"
	"syscall"
)

// execSSH replaces this process with ssh, so the terminal, signals and exit
// code all belong to ssh as if it had been run directly.
func execSSH(_ context.Context, path string, argv []string) error {
	return syscall.Exec(path, argv, os.Environ())
}
