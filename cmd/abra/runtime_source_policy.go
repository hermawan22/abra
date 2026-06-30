package main

import (
	"errors"
	"os"
	"strings"
)

func validateRuntimeSourceDownloadPolicy() error {
	sourceURL := strings.TrimSpace(os.Getenv("ABRA_SOURCE_URL"))
	if sourceURL != "" {
		if isMutableRuntimeSourceURL(sourceURL) && !truthyEnv("ABRA_ALLOW_MUTABLE_RUNTIME_SOURCE") {
			return errors.New("refusing to download mutable runtime source URL; set ABRA_SOURCE_URL to a pinned archive with ABRA_SOURCE_SHA256, or set ABRA_ALLOW_MUTABLE_RUNTIME_SOURCE=1 for local development only")
		}
		return nil
	}
	if runtimeVersion() != "main" {
		return nil
	}
	if truthyEnv("ABRA_ALLOW_MUTABLE_RUNTIME_SOURCE") {
		return nil
	}
	return errors.New("refusing to download mutable main-branch runtime source; install a tagged release, set ABRA_SOURCE_URL with ABRA_SOURCE_SHA256, or set ABRA_ALLOW_MUTABLE_RUNTIME_SOURCE=1 for local development only")
}

func isMutableRuntimeSourceURL(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	mutablePatterns := []string{
		"/archive/refs/heads/",
		"/tarball/",
		"/zipball/",
		"codeload.github.com/",
	}
	for _, pattern := range mutablePatterns {
		if strings.Contains(normalized, pattern) && strings.Contains(normalized, "main") {
			return true
		}
	}
	return strings.HasSuffix(normalized, "/archive/main.tar.gz") || strings.HasSuffix(normalized, "/archive/main.zip")
}
