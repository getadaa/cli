//go:build !darwin && !linux && !windows

package sysinfo

import "context"

func collectPlatform(ctx context.Context, info *Info) {}
