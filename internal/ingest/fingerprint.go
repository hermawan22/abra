package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const hashPrefix = "sha256:"

func Checksum(content []byte) string {
	sum := sha256.Sum256(content)
	return hashPrefix + hex.EncodeToString(sum[:])
}

func Fingerprint(sourceID, relPath, checksum string) string {
	parts := []string{
		strings.TrimSpace(sourceID),
		normalizePath(relPath),
		strings.TrimSpace(checksum),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hashPrefix + hex.EncodeToString(sum[:])
}
