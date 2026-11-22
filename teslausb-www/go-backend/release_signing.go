package main

import (
	"bytes"
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// releaseSigningKey is the minisign public key that updates must be signed
// with. The private key is kept offline and never reaches GitHub, so control
// of the repository or its releases is not enough to publish an update.
//
//go:embed release-signing.pub
var releaseSigningKey string

// releaseCommentPrefix starts the signed comment. Binding the signature to the
// tag stops an old, correctly signed release being served under a newer tag.
const releaseCommentPrefix = "dashvault-release "

const (
	minisignKeyIDLength     = 8
	minisignPublicKeyLength = 2 + minisignKeyIDLength + ed25519.PublicKeySize
	minisignSignatureLength = 2 + minisignKeyIDLength + ed25519.SignatureSize
)

func parseMinisignPublicKey(text string) ([]byte, ed25519.PublicKey, error) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "untrusted comment:") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return nil, nil, fmt.Errorf("public key: %w", err)
		}
		if len(raw) != minisignPublicKeyLength || string(raw[:2]) != "Ed" {
			return nil, nil, errors.New("public key: unsupported format")
		}
		return raw[2 : 2+minisignKeyIDLength], ed25519.PublicKey(raw[2+minisignKeyIDLength:]), nil
	}
	return nil, nil, errors.New("public key: empty")
}

// verifyReleaseSignature checks a minisign signature of SHA256SUMS and that it
// was made for this release tag. Only the prehashed format is accepted.
func verifyReleaseSignature(sums, signature []byte, tag string) error {
	keyID, publicKey, err := parseMinisignPublicKey(releaseSigningKey)
	if err != nil {
		return err
	}

	lines := strings.Split(strings.TrimRight(string(signature), "\n"), "\n")
	if len(lines) != 4 ||
		!strings.HasPrefix(lines[0], "untrusted comment:") ||
		!strings.HasPrefix(lines[2], "trusted comment: ") {
		return errors.New("signature: malformed file")
	}
	signatureBlob, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil || len(signatureBlob) != minisignSignatureLength {
		return errors.New("signature: malformed value")
	}
	if string(signatureBlob[:2]) != "ED" {
		return errors.New("signature: unsupported algorithm")
	}
	if !bytes.Equal(signatureBlob[2:2+minisignKeyIDLength], keyID) {
		return errors.New("signature: made with a different key")
	}
	fileSignature := signatureBlob[2+minisignKeyIDLength:]

	digest := blake2b.Sum512(sums)
	if !ed25519.Verify(publicKey, digest[:], fileSignature) {
		return errors.New("signature: does not match SHA256SUMS")
	}

	trustedComment := strings.TrimPrefix(lines[2], "trusted comment: ")
	globalSignature, err := base64.StdEncoding.DecodeString(lines[3])
	if err != nil || len(globalSignature) != ed25519.SignatureSize {
		return errors.New("signature: malformed trusted comment signature")
	}
	signedComment := append(append([]byte{}, fileSignature...), trustedComment...)
	if !ed25519.Verify(publicKey, signedComment, globalSignature) {
		return errors.New("signature: trusted comment was altered")
	}
	if trustedComment != releaseCommentPrefix+tag {
		return fmt.Errorf("signature: made for %q, not release %s", trustedComment, tag)
	}
	return nil
}

func parseReleaseVersion(value string) (numbers [3]int, prerelease string, ok bool) {
	if !strings.HasPrefix(value, "v") {
		return numbers, "", false
	}
	core, prerelease, hasPrerelease := strings.Cut(value[1:], "-")
	if hasPrerelease && prerelease == "" {
		return numbers, "", false
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return numbers, "", false
	}
	for i, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return numbers, "", false
		}
		number, err := strconv.Atoi(part)
		if err != nil {
			return numbers, "", false
		}
		numbers[i] = number
	}
	return numbers, prerelease, true
}

// isNewerRelease reports whether candidate should replace current. A board
// whose current version is not a release (a development or source install) can
// take any valid release; a release board only moves forward.
func isNewerRelease(candidate, current string) bool {
	candidateNumbers, candidatePrerelease, ok := parseReleaseVersion(candidate)
	if !ok {
		return false
	}
	currentNumbers, currentPrerelease, ok := parseReleaseVersion(current)
	if !ok {
		return true
	}
	for i := range candidateNumbers {
		if candidateNumbers[i] != currentNumbers[i] {
			return candidateNumbers[i] > currentNumbers[i]
		}
	}
	switch {
	case candidatePrerelease == currentPrerelease:
		return false
	case candidatePrerelease == "":
		return true
	case currentPrerelease == "":
		return false
	}
	return candidatePrerelease > currentPrerelease
}
