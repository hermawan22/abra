package ingest

import "testing"

func TestChecksumAndFingerprintAreStable(t *testing.T) {
	firstChecksum := Checksum([]byte("hello"))
	secondChecksum := Checksum([]byte("hello"))
	if firstChecksum != secondChecksum {
		t.Fatalf("checksum is not stable: %q != %q", firstChecksum, secondChecksum)
	}
	if firstChecksum == Checksum([]byte("hello!")) {
		t.Fatal("checksum did not change when content changed")
	}

	firstFingerprint := Fingerprint("docs", "./README.md", firstChecksum)
	secondFingerprint := Fingerprint("docs", "README.md", firstChecksum)
	if firstFingerprint != secondFingerprint {
		t.Fatalf("fingerprint did not normalize paths: %q != %q", firstFingerprint, secondFingerprint)
	}
	if firstFingerprint == Fingerprint("docs", "README.md", Checksum([]byte("other"))) {
		t.Fatal("fingerprint did not change when checksum changed")
	}
}
