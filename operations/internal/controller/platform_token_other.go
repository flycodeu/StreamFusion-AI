//go:build !windows

package controller

func validatePlatformTokenAccess(string) error { return nil }
