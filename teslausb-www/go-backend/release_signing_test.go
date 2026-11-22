package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/blake2b"
)

// Made by minisign 0.12 with a throwaway key, so this checks real tool output.
const (
	minisignVectorPublicKey = "untrusted comment: minisign public key 2488479648843268\n" +
		"RWRoMoRIlkeIJAppSdCUT8gN6z+4AWMjyz68CWSp0TKaqDAABviF76vx\n"
	minisignVectorSums      = "abc123  teslausb-update.tar.gz\n"
	minisignVectorSignature = "untrusted comment: signature from minisign secret key\n" +
		"RURoMoRIlkeIJEEQGTTVr7Zd4+dWn1bnayfR4cK8ZFFL56YLYDAQxM55iic0xTzx75e/a+7AtL+kDgKPT+Vh4dPUsXcj0aUCgQA=\n" +
		"trusted comment: dashvault-release v1.2.3\n" +
		"HEMryuE3QzHbsBjb7GqjpoJxrsjUACZbsa8oXRcGCoUZP3Y21G9r8Q3UiyfxRx102wJuBVcfBn6l0hTLqD7MAw==\n"
)

func useSigningKey(t *testing.T, key string) {
	t.Helper()
	original := releaseSigningKey
	releaseSigningKey = key
	t.Cleanup(func() { releaseSigningKey = original })
}

type testSigner struct {
	privateKey ed25519.PrivateKey
	keyID      []byte
}

func newTestSigner(t *testing.T) testSigner {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyID := []byte("testkey1")
	raw := append(append([]byte("Ed"), keyID...), publicKey...)
	useSigningKey(t, "untrusted comment: test key\n"+base64.StdEncoding.EncodeToString(raw)+"\n")
	return testSigner{privateKey: privateKey, keyID: keyID}
}

func (s testSigner) sign(data []byte, comment string) []byte {
	digest := blake2b.Sum512(data)
	fileSignature := ed25519.Sign(s.privateKey, digest[:])
	blob := append(append([]byte("ED"), s.keyID...), fileSignature...)
	global := ed25519.Sign(s.privateKey, append(append([]byte{}, fileSignature...), comment...))
	return []byte(fmt.Sprintf("untrusted comment: test\n%s\ntrusted comment: %s\n%s\n",
		base64.StdEncoding.EncodeToString(blob), comment, base64.StdEncoding.EncodeToString(global)))
}

func TestReleaseSigningKeyIsTheProjectKey(t *testing.T) {
	keyID, publicKey, err := parseMinisignPublicKey(releaseSigningKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(keyID); got != "200e1e2f85758cdf" {
		t.Errorf("unexpected key id %s", got)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		t.Errorf("unexpected key length %d", len(publicKey))
	}
}

func TestVerifyReleaseSignature_realMinisignFile(t *testing.T) {
	useSigningKey(t, minisignVectorPublicKey)
	if err := verifyReleaseSignature([]byte(minisignVectorSums), []byte(minisignVectorSignature), "v1.2.3"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyReleaseSignature_refusals(t *testing.T) {
	sums := []byte(minisignVectorSums)
	signature := minisignVectorSignature
	lines := strings.Split(signature, "\n")
	legacy := strings.Replace(lines[1], "RUR", "RWR", 1)

	cases := []struct {
		name, key, sums, signature, tag string
	}{
		{"changed checksums", minisignVectorPublicKey, "000000  teslausb-update.tar.gz\n", signature, "v1.2.3"},
		{"other release", minisignVectorPublicKey, string(sums), signature, "v1.2.4"},
		{"altered comment", minisignVectorPublicKey, string(sums),
			strings.Replace(signature, "release v1.2.3", "release v1.2.4", 1), "v1.2.4"},
		{"legacy format", minisignVectorPublicKey, string(sums),
			strings.Replace(signature, lines[1], legacy, 1), "v1.2.3"},
		{"different key", "untrusted comment: other\nRWQgDh4vhXWM3w8rsMoew4ZBOz6abx/qY0KwCnRAK73rf2huszUBUbX9\n",
			string(sums), signature, "v1.2.3"},
		{"empty signature", minisignVectorPublicKey, string(sums), "", "v1.2.3"},
		{"truncated signature", minisignVectorPublicKey, string(sums), strings.Join(lines[:2], "\n"), "v1.2.3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useSigningKey(t, tc.key)
			if err := verifyReleaseSignature([]byte(tc.sums), []byte(tc.signature), tc.tag); err == nil {
				t.Fatal("expected the signature to be refused")
			}
		})
	}
}

func TestIsNewerRelease(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"v0.1.1", "v0.1.0", true},
		{"v0.2.0", "v0.1.9", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.10.0", "v0.9.0", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.1.1", false},
		{"v0.1.1", "v0.1.1-test1", true},
		{"v0.1.1-test1", "v0.1.1", false},
		{"v0.1.1-test2", "v0.1.1-test1", true},
		{"v0.1.1", "dev", true},
		{"v0.1.1", "source-20261122", true},
		{"latest", "v0.1.0", false},
		{"v1.2", "v0.1.0", false},
		{"v1.2.x", "v0.1.0", false},
		{"v1.2.3-", "v0.1.0", false},
		{"1.2.3", "v0.1.0", false},
	}
	for _, tc := range cases {
		if got := isNewerRelease(tc.candidate, tc.current); got != tc.want {
			t.Errorf("isNewerRelease(%q, %q) = %v, want %v", tc.candidate, tc.current, got, tc.want)
		}
	}
}

type signedRelease struct {
	tag                string
	tarball, sums, sig []byte
	omitSignature      bool
}

func (r signedRelease) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/releases/latest":
			base := "http://" + req.Host + "/dl/"
			assets := []map[string]string{
				{"name": "teslausb-update.tar.gz", "browser_download_url": base + "teslausb-update.tar.gz"},
				{"name": "SHA256SUMS", "browser_download_url": base + "SHA256SUMS"},
			}
			if !r.omitSignature {
				assets = append(assets, map[string]string{"name": "SHA256SUMS.minisig", "browser_download_url": base + "SHA256SUMS.minisig"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": r.tag, "assets": assets})
		case "/dl/teslausb-update.tar.gz":
			_, _ = w.Write(r.tarball)
		case "/dl/SHA256SUMS":
			_, _ = w.Write(r.sums)
		case "/dl/SHA256SUMS.minisig":
			_, _ = w.Write(r.sig)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newSignedRelease(t *testing.T, signer testSigner, tag, packagedVersion string) signedRelease {
	t.Helper()
	tarball, hash := buildTarGz(t, "VERSION", packagedVersion+"\n")
	sums := []byte(hash + "  teslausb-update.tar.gz\n")
	return signedRelease{tag: tag, tarball: tarball, sums: sums, sig: signer.sign(sums, releaseCommentPrefix+tag)}
}

func installRelease(t *testing.T, r signedRelease) (installed bool, err error) {
	t.Helper()
	srv := r.serve(t)
	runOTAOperation = func(string, string, ...string) error {
		installed = true
		return nil
	}
	err = downloadVerifyAndInstall(r.tag, srv.URL+"/dl/teslausb-update.tar.gz", srv.URL+"/dl/SHA256SUMS",
		srv.URL+"/dl/SHA256SUMS.minisig", otaLogFile, otaDownloadPath, otaExtractDir, otaInstallScript)
	return installed, err
}

func TestUpdatePipeline_installsSignedRelease(t *testing.T) {
	redirectOTAPaths(t)
	stubRemount(t)
	signer := newTestSigner(t)

	installed, err := installRelease(t, newSignedRelease(t, signer, "v2.0.0", "v2.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("a valid release was not installed")
	}
	log, _ := os.ReadFile(otaLogFile)
	if !strings.Contains(string(log), "Signature OK") {
		t.Errorf("log does not record the signature check: %s", log)
	}
}

func TestUpdatePipeline_refusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, testSigner, *signedRelease)
	}{
		{"changed tarball", func(_ *testing.T, _ testSigner, r *signedRelease) {
			r.tarball = append(append([]byte{}, r.tarball...), 0)
		}},
		{"changed checksums", func(_ *testing.T, _ testSigner, r *signedRelease) {
			r.sums = []byte(strings.Repeat("0", 64) + "  teslausb-update.tar.gz\n")
		}},
		{"signature from another key", func(_ *testing.T, _ testSigner, r *signedRelease) {
			_, other, _ := ed25519.GenerateKey(rand.Reader)
			r.sig = testSigner{privateKey: other, keyID: []byte("testkey1")}.sign(r.sums, releaseCommentPrefix+r.tag)
		}},
		{"old release under a new tag", func(_ *testing.T, s testSigner, r *signedRelease) {
			r.sig = s.sign(r.sums, releaseCommentPrefix+"v1.0.0")
		}},
		{"packaged version differs from tag", func(t *testing.T, s testSigner, r *signedRelease) {
			tarball, hash := buildTarGz(t, "VERSION", "v1.0.0\n")
			r.tarball = tarball
			r.sums = []byte(hash + "  teslausb-update.tar.gz\n")
			r.sig = s.sign(r.sums, releaseCommentPrefix+r.tag)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			redirectOTAPaths(t)
			stubRemount(t)
			signer := newTestSigner(t)
			release := newSignedRelease(t, signer, "v2.0.0", "v2.0.0")
			tc.mutate(t, signer, &release)

			installed, err := installRelease(t, release)
			if err == nil {
				t.Fatal("expected the update to be refused")
			}
			if installed {
				t.Fatal("a refused update reached the installer")
			}
		})
	}
}

func applyRelease(t *testing.T, r signedRelease) *httptest.ResponseRecorder {
	t.Helper()
	srv := r.serve(t)
	latest, err := fetchLatestRelease(srv.URL + "/releases/latest")
	if err != nil {
		t.Fatal(err)
	}
	setOTAEnv(t, true, srv.URL+"/releases/latest")
	otaCache.mu.Lock()
	otaCache.latest = latest
	otaCache.mu.Unlock()
	t.Cleanup(func() {
		otaCache.mu.Lock()
		otaCache.latest = nil
		otaCache.mu.Unlock()
	})
	w := httptest.NewRecorder()
	HandleUpdateApply(w, httptest.NewRequest(http.MethodPost, "/api/update/apply", nil))
	return w
}

func TestHandleUpdateApply_refusesUnsignedRelease(t *testing.T) {
	redirectOTAPaths(t)
	signer := newTestSigner(t)
	release := newSignedRelease(t, signer, "v2.0.0", "v2.0.0")
	release.omitSignature = true

	w := applyRelease(t, release)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "not signed") {
		t.Fatalf("got %d: %s", w.Code, w.Body)
	}
}

func TestHandleUpdateApply_refusesDowngrade(t *testing.T) {
	redirectOTAPaths(t)
	signer := newTestSigner(t)
	originalVersion := version
	version = "v3.0.0"
	t.Cleanup(func() { version = originalVersion })

	w := applyRelease(t, newSignedRelease(t, signer, "v2.0.0", "v2.0.0"))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "not newer") {
		t.Fatalf("got %d: %s", w.Code, w.Body)
	}
}
