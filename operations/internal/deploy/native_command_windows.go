//go:build !linux

package deploy

import (
	"context"
	"errors"
	"os"
)

func runNativeBuild(context.Context, string, string, string, ...string) ([]byte, error) {
	return nil, errors.New("LINUX_ONLY")
}

func nativeFileOwner(os.FileInfo) (int, int) { return 0, 0 }
