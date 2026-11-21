package main

// rsync_key.go — SSH keypair management for the rsync archive backend.
//
// The key pair lives at /root/.ssh/id_ed25519 (private) and id_ed25519.pub
// (public).  This is the standard root SSH identity location, so rsync over
// SSH picks it up automatically — no extra SSH config needed.
//
// API surface:
//
//	GET  /api/config/rsync/ssh-key    — public key + metadata
//	POST /api/config/rsync/keygen     — generate new ed25519 keypair (overwrites)
//	POST /api/config/rsync/import-key — import base64-encoded PEM private key

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

const (
	rsyncSSHDir  = "/root/.ssh"
	rsyncPrivKey = "/root/.ssh/id_ed25519"
	rsyncPubKey  = "/root/.ssh/id_ed25519.pub"
)

// RsyncSSHKeyInfo is the GET response and the body returned by keygen/import.
type RsyncSSHKeyInfo struct {
	HasKey    bool   `json:"has_key"`
	PublicKey string `json:"public_key,omitempty"` // full "ssh-ed25519 AAA… root@teslausb" line
	KeyType   string `json:"key_type,omitempty"`   // "ed25519" or "rsa"
}

// readRsyncSSHKey reads the public key from disk.
// Tries ed25519 first, falls back to rsa.
func readRsyncSSHKey() RsyncSSHKeyInfo {
	candidates := []struct{ pub, typ string }{
		{rsyncPubKey, "ed25519"},
		{"/root/.ssh/id_rsa.pub", "rsa"},
	}
	for _, c := range candidates {
		data, err := os.ReadFile(c.pub)
		if err != nil {
			continue
		}
		pub := strings.TrimSpace(string(data))
		if pub == "" {
			continue
		}
		return RsyncSSHKeyInfo{HasKey: true, PublicKey: pub, KeyType: c.typ}
	}
	return RsyncSSHKeyInfo{HasKey: false}
}

// HandleRsyncSSHKey — GET /api/config/rsync/ssh-key
func HandleRsyncSSHKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(readRsyncSSHKey())
}

// HandleRsyncKeygen — POST /api/config/rsync/keygen
// Generates a fresh ed25519 keypair (no passphrase), overwriting any existing key.
func HandleRsyncKeygen(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	err := withConfRW(func() error {
		if err := os.MkdirAll(rsyncSSHDir, 0o700); err != nil {
			return err
		}
		// Remove existing key files so ssh-keygen doesn't prompt for overwrite.
		_ = os.Remove(rsyncPrivKey)
		_ = os.Remove(rsyncPubKey)
		return exec.Command(
			"ssh-keygen", "-t", "ed25519",
			"-f", rsyncPrivKey,
			"-N", "", // no passphrase
			"-C", "root@teslausb",
		).Run()
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "keygen failed: "+err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(readRsyncSSHKey())
}

// HandleRsyncImportKey — POST /api/config/rsync/import-key
//
// Request body: {"private_key_b64": "<standard or URL-safe base64 of PEM>"}
//
// The handler decodes the base64, writes the PEM as the private key, then
// uses "ssh-keygen -y" to derive and store the public key.
func HandleRsyncImportKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		PrivateKeyB64 string `json:"private_key_b64"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(req.PrivateKeyB64) == "" {
		writeJSONError(w, http.StatusBadRequest, "private_key_b64 is required")
		return
	}

	// Accept both standard and URL-safe base64 (and strip whitespace).
	b64clean := strings.ReplaceAll(strings.TrimSpace(req.PrivateKeyB64), "\n", "")
	pem, err := base64.StdEncoding.DecodeString(b64clean)
	if err != nil {
		pem, err = base64.URLEncoding.DecodeString(b64clean)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid base64: "+err.Error())
			return
		}
	}

	pemStr := string(pem)
	if !strings.Contains(pemStr, "-----BEGIN") || !strings.Contains(pemStr, "PRIVATE KEY") {
		writeJSONError(w, http.StatusBadRequest, "does not look like a PEM private key")
		return
	}

	err = withConfRW(func() error {
		if err := os.MkdirAll(rsyncSSHDir, 0o700); err != nil {
			return err
		}
		if err := writeFileAtomic(rsyncPrivKey, pem, 0o600); err != nil {
			return err
		}
		// Derive matching public key from the private key.
		out, err := exec.Command("ssh-keygen", "-y", "-f", rsyncPrivKey).Output()
		if err != nil {
			return fmt.Errorf("could not derive public key (bad key?): %w", err)
		}
		return writeFileAtomic(rsyncPubKey, out, 0o644)
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "import failed: "+err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(readRsyncSSHKey())
}
