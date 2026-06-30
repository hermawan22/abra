package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const runtimeSourceProvenanceFile = ".abra-runtime-source.json"

type runtimeSourceProvenance struct {
	Version    string `json:"version"`
	Mode       string `json:"mode"`
	SourceURL  string `json:"source_url"`
	SourceSHA  string `json:"source_sha256,omitempty"`
	Asset      string `json:"asset,omitempty"`
	Unverified bool   `json:"unverified,omitempty"`
}

func runtimeVersion() string {
	if strings.TrimSpace(version) != "" && version != "dev" {
		return strings.TrimSpace(version)
	}
	return "main"
}

func runtimeSourceURL() string {
	if value := strings.TrimSpace(os.Getenv("ABRA_SOURCE_URL")); value != "" {
		return value
	}
	if runtimeVersion() == "main" {
		return "https://github.com/hermawan22/abra/archive/refs/heads/main.tar.gz"
	}
	return runtimeReleaseAssetURL(runtimeBundleAssetName())
}

func expectedRuntimeSourceProvenance() runtimeSourceProvenance {
	sourceURL := runtimeSourceURL()
	if strings.TrimSpace(os.Getenv("ABRA_SOURCE_URL")) != "" {
		return runtimeSourceProvenance{
			Version:    runtimeVersion(),
			Mode:       "source_url",
			SourceURL:  sourceURL,
			SourceSHA:  normalizedRuntimeSourceSHA(),
			Unverified: strings.TrimSpace(os.Getenv("ABRA_SOURCE_SHA256")) == "" && truthyEnv("ABRA_ALLOW_UNVERIFIED_SOURCE_URL"),
		}
	}
	if runtimeVersion() == "main" {
		return runtimeSourceProvenance{Version: runtimeVersion(), Mode: "mutable_main", SourceURL: sourceURL, Unverified: true}
	}
	return runtimeSourceProvenance{Version: runtimeVersion(), Mode: "release", SourceURL: sourceURL, Asset: runtimeBundleAssetName()}
}

func normalizedRuntimeSourceSHA() string {
	expected := strings.TrimSpace(os.Getenv("ABRA_SOURCE_SHA256"))
	if expected == "" {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(expected, "sha256:"))
}

func runtimeSourceCacheValid(dir string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, runtimeSourceProvenanceFile))
	if err != nil {
		return false
	}
	var cached runtimeSourceProvenance
	if err := json.Unmarshal(raw, &cached); err != nil {
		return false
	}
	return cached == expectedRuntimeSourceProvenance()
}

func writeRuntimeSourceProvenance(dir string) error {
	raw, err := json.MarshalIndent(expectedRuntimeSourceProvenance(), "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(filepath.Join(dir, runtimeSourceProvenanceFile), raw, 0o644)
}
