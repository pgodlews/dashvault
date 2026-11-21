package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchiveValuesRejectOptionAndLineInjection(t *testing.T) {
	bad := map[string]ArchiveConfig{
		"rsync user as ssh option":   {Rsync: RsyncBackendConfig{User: "-oProxyCommand=touch /tmp/x"}},
		"rsync user with at sign":    {Rsync: RsyncBackendConfig{User: "a@b"}},
		"rsync server as option":     {Rsync: RsyncBackendConfig{Server: "-oProxyCommand=x"}},
		"rsync server with space":    {Rsync: RsyncBackendConfig{Server: "host -e sh"}},
		"rsync path with newline":    {Rsync: RsyncBackendConfig{Path: "/data\nexport ARCHIVE_SYSTEM=none"}},
		"rclone remote as option":    {Rclone: RcloneBackendConfig{Drive: "--config=/tmp/evil"}},
		"rclone remote with colon":   {Rclone: RcloneBackendConfig{Drive: "gdrive:"}},
		"rclone path with newline":   {Rclone: RcloneBackendConfig{Path: "a\nb"}},
		"cifs server as option":      {CIFS: CIFSBackendConfig{Server: "-h"}},
		"share with carriage return": {CIFS: CIFSBackendConfig{Share: "a\rb"}},
		"password with tab":          {CIFS: CIFSBackendConfig{Password: "a\tb"}},
		"unicode line separator":     {NFS: NFSBackendConfig{Path: "/a\u2028b"}},
		"invalid utf-8":              {CIFS: CIFSBackendConfig{User: "a\xffb"}},
		"escape character":           {CIFS: CIFSBackendConfig{Domain: "a\x1bb"}},
	}
	for name, cfg := range bad {
		if validateArchiveMountValues(cfg) == nil {
			t.Errorf("%s: accepted %+v", name, cfg)
		}
	}
	good := []ArchiveConfig{
		{},
		{System: "rsync", Rsync: RsyncBackendConfig{User: "backup.user-1", Server: "nas.local", Path: "/volume1/Tesla Cam"}},
		{System: "rsync", Rsync: RsyncBackendConfig{User: "pi", Server: "192.168.1.20", Path: "clips"}},
		{System: "rclone", Rclone: RcloneBackendConfig{Drive: "my drive+1@work", Path: "Tesla/Cam"}},
		{System: "cifs", CIFS: CIFSBackendConfig{Server: "nas", Share: "Tesla Cam/sub", User: "DOMAIN\\user", Password: "p'a\"ss $word; `x`", Domain: "WORKGROUP"}},
		{System: "nfs", NFS: NFSBackendConfig{Server: "fd00::1", Path: "/volume1/żółw"}},
	}
	for _, cfg := range good {
		if err := validateArchiveMountValues(cfg); err != nil {
			t.Errorf("rejected %+v: %v", cfg, err)
		}
	}
}

func TestUpsertConfKeyKeepsValuesOnOneLine(t *testing.T) {
	content := upsertConfKey("export A='1'\n", "RSYNC_PATH", "/data\nexport ARCHIVE_SYSTEM='none'\r\x00\u2028")
	if strings.Count(content, "\n") != 2 || strings.ContainsAny(content, "\r\x00\u2028") {
		t.Fatalf("value escaped its line: %q", content)
	}
	if got := readConfKey(content, "RSYNC_PATH"); got != "/dataexport ARCHIVE_SYSTEM='none'" {
		t.Fatalf("RSYNC_PATH = %q", got)
	}
	if got := readConfKey(content, "ARCHIVE_SYSTEM"); got != "" {
		t.Fatalf("injected ARCHIVE_SYSTEM = %q", got)
	}
}

func archiveRequest(t *testing.T, method, body string) (int, ArchiveConfig, string) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/config/archive", strings.NewReader(body))
	rec := httptest.NewRecorder()
	HandleArchiveConfig(rec, req)
	var cfg ArchiveConfig
	_ = json.Unmarshal(rec.Body.Bytes(), &cfg)
	return rec.Code, cfg, rec.Body.String()
}

func TestArchiveConfigNeverReturnsSharePassword(t *testing.T) {
	stubArchiveEnvironment(t)
	stubRemount(t)
	original := confFilePath
	t.Cleanup(func() { confFilePath = original })
	confFilePath = filepath.Join(t.TempDir(), "config.conf")
	if err := os.WriteFile(confFilePath, []byte("export ARCHIVE_SYSTEM='none'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := func() string {
		data, err := os.ReadFile(confFilePath)
		if err != nil {
			t.Fatal(err)
		}
		return readConfKey(string(data), "SHARE_PASSWORD")
	}

	code, cfg, body := archiveRequest(t, http.MethodPost, `{"system":"cifs","saved_clips":true,"cifs":{"server":"nas","share":"cam","user":"pi","password":"s3cret-value"}}`)
	if code != http.StatusOK {
		t.Fatalf("save: %d %s", code, body)
	}
	if strings.Contains(body, "s3cret-value") || cfg.CIFS.Password != "" || !cfg.CIFS.PasswordSet {
		t.Fatalf("POST response exposes the password or lacks password_set: %s", body)
	}
	if saved() != "s3cret-value" {
		t.Fatalf("password not saved: %q", saved())
	}

	code, cfg, body = archiveRequest(t, http.MethodGet, "")
	if code != http.StatusOK || strings.Contains(body, "s3cret-value") || !cfg.CIFS.PasswordSet {
		t.Fatalf("GET exposes the password or lacks password_set: %d %s", code, body)
	}

	// The form posts back what it was given: an empty password keeps the saved one.
	code, _, body = archiveRequest(t, http.MethodPost, `{"system":"cifs","saved_clips":true,"cifs":{"server":"nas2","share":"cam","user":"pi","password":""}}`)
	if code != http.StatusOK || saved() != "s3cret-value" {
		t.Fatalf("empty password did not keep the saved one: %d %s saved=%q", code, body, saved())
	}
	creds, _ := os.ReadFile(cifsCredentialsFilePath)
	if !strings.Contains(string(creds), "password=s3cret-value\n") {
		t.Fatalf("credentials file lost the password: %q", creds)
	}

	code, cfg, body = archiveRequest(t, http.MethodPost, `{"system":"cifs","saved_clips":true,"cifs":{"server":"nas2","share":"cam","user":"guest","password":"","clear_password":true}}`)
	if code != http.StatusOK || saved() != "" || cfg.CIFS.PasswordSet {
		t.Fatalf("clear_password did not remove it: %d %s saved=%q", code, body, saved())
	}
}

func TestArchiveConfigRejectsInjectionBeforeWriting(t *testing.T) {
	stubArchiveEnvironment(t)
	stubRemount(t)
	original := confFilePath
	t.Cleanup(func() { confFilePath = original })
	confFilePath = filepath.Join(t.TempDir(), "config.conf")
	before := "export ARCHIVE_SYSTEM='none'\n"
	if err := os.WriteFile(confFilePath, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"system":"rsync","rsync":{"user":"-oProxyCommand=id","server":"nas","path":"/x"}}`,
		`{"system":"rsync","rsync":{"user":"pi","server":"nas","path":"/x\nexport FOO=1"}}`,
		`{"system":"rclone","rclone":{"drive":"--config=/tmp/x","path":"p"}}`,
	} {
		if code, _, response := archiveRequest(t, http.MethodPost, body); code != http.StatusBadRequest {
			t.Errorf("%s: status %d %s", body, code, response)
		}
	}
	if data, _ := os.ReadFile(confFilePath); string(data) != before {
		t.Fatalf("config changed by rejected requests: %q", data)
	}
}

func TestSetupRequestValidation(t *testing.T) {
	bad := []SetupApplyRequest{
		{ArchiveType: "cifs\nexport X=1", ArchiveHost: "nas", ShareName: "cam"},
		{ArchiveType: "ftp", ArchiveHost: "nas", ShareName: "cam"},
		{ArchiveType: "rsync", ArchiveHost: "nas", ShareName: "/x", Username: "-oProxyCommand=id"},
		{ArchiveType: "rsync", ArchiveHost: "-nas", ShareName: "/x", Username: "pi"},
		{ArchiveType: "rclone", ArchiveHost: "--config=/x", ShareName: "p"},
		{ArchiveType: "cifs", ArchiveHost: "nas", ShareName: "cam", Password: "a\nb"},
		{ArchiveType: "none", ArchiveHost: "x\ny"},
	}
	for _, req := range bad {
		if validateSetupRequest(req) == nil {
			t.Errorf("accepted %+v", req)
		}
	}
	good := []SetupApplyRequest{
		{ArchiveType: "none"},
		{ArchiveType: "cifs", ArchiveHost: "nas.local", ShareName: "Tesla Cam", Username: "pi", Password: "p'w d"},
		{ArchiveType: "nfs", ArchiveHost: "10.0.0.2", ShareName: "/volume1/cam"},
		{ArchiveType: "rsync", ArchiveHost: "nas", ShareName: "/x", Username: "pi"},
		{ArchiveType: "rclone", ArchiveHost: "gdrive", ShareName: "cam"},
	}
	for _, req := range good {
		if err := validateSetupRequest(req); err != nil {
			t.Errorf("rejected %+v: %v", req, err)
		}
	}
}

func TestSetupApplyRejectsInjectionBeforeWriting(t *testing.T) {
	stubRemount(t)
	original := setupConfFile
	t.Cleanup(func() { setupConfFile = original })
	setupConfFile = filepath.Join(t.TempDir(), "config.conf")
	req := httptest.NewRequest(http.MethodPost, "/api/setup/apply", strings.NewReader(`{"archive_type":"rsync","archive_host":"nas","share_name":"/x","username":"-oProxyCommand=id"}`))
	rec := httptest.NewRecorder()
	HandleSetupApply(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(setupConfFile); !os.IsNotExist(err) {
		t.Fatal("configuration was written for a rejected request")
	}
}

func TestTelegramConfigRejectsMalformedTokenAndChat(t *testing.T) {
	stubRemount(t)
	originalPath, originalRun := telegramEnvPath, systemctlRun
	t.Cleanup(func() { telegramEnvPath, systemctlRun = originalPath, originalRun })
	telegramEnvPath = filepath.Join(t.TempDir(), "telegram.env")
	systemctlRun = func(...string) error { return nil }
	for _, body := range []string{
		`{"bot_token":"bot123:abc","chat_id":"1"}`,
		`{"bot_token":"123:abc/../x","chat_id":"1"}`,
		`{"bot_token":"123:abc\nTELEGRAM_CHAT_ID=9","chat_id":"1"}`,
		`{"bot_token":"123:abc","chat_id":"1\nTELEGRAM_ENABLED=true"}`,
		`{"bot_token":"123:abc","chat_id":"1;2"}`,
	} {
		rec := httptest.NewRecorder()
		HandleTelegramConfig(rec, httptest.NewRequest(http.MethodPost, "/api/config/telegram", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if _, err := os.Stat(telegramEnvPath); !os.IsNotExist(err) {
		t.Fatal("env file was written for a rejected request")
	}
	rec := httptest.NewRecorder()
	HandleTelegramConfig(rec, httptest.NewRequest(http.MethodPost, "/api/config/telegram", strings.NewReader(`{"bot_token":" 123456:AAE_x-y ","chat_id":"123, -1009, @channel"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid config rejected: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRequestErrorReasonOmitsTheURL(t *testing.T) {
	err := &url.Error{Op: "Post", URL: "https://api.telegram.org/bot123456:SECRET/sendMessage", Err: errors.New("dial tcp: no route to host")}
	if got := requestErrorReason(err); strings.Contains(got, "SECRET") || got != "dial tcp: no route to host" {
		t.Fatalf("reason = %q", got)
	}
	if got := requestErrorReason(errors.New("plain")); got != "plain" {
		t.Fatalf("reason = %q", got)
	}
}

func TestConfigDumpEndpointIsGone(t *testing.T) {
	h := newHTTPHandler(NewBroker(nil, time.Second), func(context.Context, string, string) (bool, error) { return true, nil })
	req := httptest.NewRequest(http.MethodGet, "/cgi-bin/config.sh?WIFIPASS", nil)
	req.SetBasicAuth("test-user", "test-password")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
}

func gateRequest(h http.HandlerFunc, ip, user, password string) int {
	req := httptest.NewRequest(http.MethodGet, authGatePath, nil)
	req.Header.Set("X-Real-IP", ip)
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec.Code
}

func TestAuthGateLocksOutRepeatedWrongPasswords(t *testing.T) {
	orig := globalRateLimiter
	t.Cleanup(func() { globalRateLimiter = orig })
	globalRateLimiter = newTestLimiter()
	t.Setenv("TESLAUSB_RATE_LIMIT_ATTEMPTS", "3")

	verify := func(_ context.Context, user, password string) (bool, error) {
		return user == "admin" && password == "right", nil
	}
	h := newHTTPHandler(NewBroker(nil, time.Second), verify)
	const attacker, owner = "192.168.1.66", "192.168.1.10"

	if got := gateRequest(h, owner, "admin", "right"); got != http.StatusNoContent {
		t.Fatalf("correct login: %d", got)
	}
	// Requests without credentials are challenges, not guesses.
	for i := 0; i < 20; i++ {
		if got := gateRequest(h, owner, "", ""); got != http.StatusUnauthorized {
			t.Fatalf("no credentials: %d", got)
		}
	}
	if got := gateRequest(h, owner, "admin", "right"); got != http.StatusNoContent {
		t.Fatalf("owner locked out by credential-less requests: %d", got)
	}

	for i := 0; i < 3; i++ {
		if got := gateRequest(h, attacker, "admin", "guess"); got != http.StatusUnauthorized {
			t.Fatalf("wrong password %d: %d", i, got)
		}
	}
	// Locked: the answer no longer depends on the password, so guessing on is useless.
	for _, password := range []string{"guess", "right"} {
		if got := gateRequest(h, attacker, "admin", password); got != http.StatusForbidden {
			t.Fatalf("locked address got %d for password %q", got, password)
		}
	}
	if got := gateRequest(h, attacker, "", ""); got != http.StatusForbidden {
		t.Fatalf("locked address without credentials: %d", got)
	}
	if got := gateRequest(h, owner, "admin", "right"); got != http.StatusNoContent {
		t.Fatalf("other address affected by the lockout: %d", got)
	}
}

func TestCachedVerifierRemembersOnlySuccessAndFollowsThePasswordFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".htpasswd")
	if err := os.WriteFile(file, []byte("admin:hash1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	valid := "right"
	verify := cachedVerifier(func(_ context.Context, _, password string) (bool, error) {
		calls++
		return password == valid, nil
	}, func() string { return file })
	check := func(password string, want bool, wantCalls int) {
		t.Helper()
		ok, err := verify(context.Background(), "admin", password)
		if err != nil || ok != want || calls != wantCalls {
			t.Fatalf("verify(%q) = %v, %v after %d calls; want %v after %d", password, ok, err, calls, want, wantCalls)
		}
	}
	check("wrong", false, 1)
	check("wrong", false, 2) // failures are never remembered
	check("right", true, 3)
	check("right", true, 3) // remembered
	check("wrong", false, 4)
	check("right", true, 4)

	// A changed password file drops the remembered login.
	valid = "new"
	if err := os.WriteFile(file, []byte("admin:hash2-longer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("right", false, 5)
	check("new", true, 6)
	check("new", true, 6)

	// Without a readable password file nothing is remembered.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	check("new", true, 7)
	check("new", true, 8)
}
