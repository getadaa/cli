//go:build windows

package cli

import (
	"context"
	"os"
	"os/exec"
)

func execSSH(ctx context.Context, path string, argv []string) error {
	c := exec.CommandContext(ctx, path, argv[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}
