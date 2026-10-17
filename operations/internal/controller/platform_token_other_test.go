//go:build !windows

package controller

import "testing"

func protectPlatformTokenPath(*testing.T, string) {}
