package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func stubArchiveEnvironment(t *testing.T) {
	t.Helper()
	oldFstab, oldCreds, oldCommand := archiveFstabPath, cifsCredentialsFilePath, archiveReconfigure
	dir := t.TempDir()
	archiveFstabPath, cifsCredentialsFilePath = dir+"/fstab", dir+"/credentials"
	archiveReconfigure = func(...string) error { return nil }
	t.Cleanup(func() { archiveFstabPath, cifsCredentialsFilePath, archiveReconfigure = oldFstab, oldCreds, oldCommand })
}

func TestArchiveDestinationTransitions(t *testing.T) {
	stubArchiveEnvironment(t)
	stubRemount(t)
	oldConf := confFilePath
	confFilePath = t.TempDir() + "/config"
	t.Cleanup(func() { confFilePath = oldConf })
	original := "# user mount\nserver:/other /mnt/other nfs defaults 0 0\n//old/A /mnt/archive cifs noauto 0 0\n"
	if err := os.WriteFile(archiveFstabPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(confFilePath, []byte("export UNRELATED='keep'\nexport MUSIC_SHARE_NAME='Music'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configs := []ArchiveConfig{
		{System: "cifs", CIFS: CIFSBackendConfig{Server: "new", Share: "B with spaces", User: "user", Password: "pass", Version: "3.0", Sec: "ntlmssp"}},
		{System: "nfs", NFS: NFSBackendConfig{Server: "nfs", Path: "/clips"}},
		{System: "cifs", CIFS: CIFSBackendConfig{Server: "new", Share: "C"}},
		{System: "none"},
		{System: "cifs", CIFS: CIFSBackendConfig{Server: "nas", Share: "D"}},
	}
	for _, cfg := range configs {
		t.Run(cfg.System+cfg.CIFS.Share, func(t *testing.T) {
			var phases []string
			archiveReconfigure = func(args ...string) error {
				phases = append(phases, strings.Join(args, " "))
				data, _ := os.ReadFile(confFilePath)
				if args[0] == "restart" && readConfKey(string(data), "ARCHIVE_SYSTEM") != cfg.System {
					t.Fatal("restart before commit")
				}
				return nil
			}
			if err := writeArchiveConfig(cfg); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(archiveFstabPath)
			fstab := string(data)
			if !strings.Contains(fstab, "server:/other /mnt/other nfs defaults 0 0") || strings.Contains(fstab, "//old/A") {
				t.Fatalf("fstab=%s", fstab)
			}
			if cfg.System == "cifs" && !strings.Contains(fstab, "//"+cfg.CIFS.Server+"/"+fstabEscape(cfg.CIFS.Share)+" /mnt/archive cifs") {
				t.Fatal(fstab)
			}
			if cfg.System == "nfs" && !strings.Contains(fstab, "nfs:/clips /mnt/archive nfs") {
				t.Fatal(fstab)
			}
			if cfg.System == "none" && strings.Contains(fstab, "/mnt/archive") {
				t.Fatal(fstab)
			}
			if strings.Join(phases, ",") != "prepare "+cfg.System+",restart" {
				t.Fatal(phases)
			}
			data, _ = os.ReadFile(confFilePath)
			if readConfKey(string(data), "UNRELATED") != "keep" {
				t.Fatal("lost unrelated key")
			}
		})
	}
	info, err := os.Stat(cifsCredentialsFilePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials: %v %v", info, err)
	}
}

func TestArchiveReconfigurationFailuresAreReported(t *testing.T) {
	for _, phase := range []string{"prepare", "restart"} {
		t.Run(phase, func(t *testing.T) {
			stubArchiveEnvironment(t)
			stubRemount(t)
			original := confFilePath
			confFilePath = writeConf(t, "export ARCHIVE_SYSTEM='none'\n")
			t.Cleanup(func() { confFilePath = original })
			archiveReconfigure = func(args ...string) error {
				if args[0] == phase {
					return fmt.Errorf("fake %s failure", phase)
				}
				return nil
			}
			cfg := ArchiveConfig{System: "cifs", CIFS: CIFSBackendConfig{Server: "nas", Share: "clips"}}
			body, _ := json.Marshal(cfg)
			w := httptest.NewRecorder()
			HandleArchiveConfig(w, httptest.NewRequest("POST", "/api/config/archive", strings.NewReader(string(body))))
			if w.Code != 500 || !strings.Contains(w.Body.String(), phase) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			data, _ := os.ReadFile(confFilePath)
			want := "cifs"
			if phase == "prepare" {
				want = "none"
			}
			if readConfKey(string(data), "ARCHIVE_SYSTEM") != want {
				t.Fatalf("config on failure: %s", data)
			}
		})
	}
}

func TestArchiveRejectsCredentialAndOptionInjection(t *testing.T) {
	for _, cfg := range []ArchiveConfig{
		{CIFS: CIFSBackendConfig{Password: "a\nusername=other"}},
		{CIFS: CIFSBackendConfig{Version: "3.0,credentials=/other"}},
		{NFS: NFSBackendConfig{Server: "host\n#"}},
	} {
		if validateArchiveMountValues(cfg) == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
}

func TestArchiveWriteFailureRestoresFilesAndDoesNotRestart(t *testing.T) {
	stubArchiveEnvironment(t)
	stubRemount(t)
	original := confFilePath
	confFilePath = t.TempDir() + "/missing-parent/config"
	t.Cleanup(func() { confFilePath = original })
	fstab := "//old/share /mnt/archive cifs noauto 0 0\n"
	if err := os.WriteFile(archiveFstabPath, []byte(fstab), 0o644); err != nil {
		t.Fatal(err)
	}
	phases := []string{}
	archiveReconfigure = func(args ...string) error { phases = append(phases, args[0]); return nil }
	cfg := ArchiveConfig{System: "cifs", CIFS: CIFSBackendConfig{Server: "new", Share: "share"}}
	if err := writeArchiveConfig(cfg); err == nil {
		t.Fatal("expected write failure")
	}
	data, _ := os.ReadFile(archiveFstabPath)
	if string(data) != fstab {
		t.Fatalf("fstab not restored: %s", data)
	}
	if _, err := os.Stat(cifsCredentialsFilePath); !os.IsNotExist(err) {
		t.Fatalf("new credentials not removed: %v", err)
	}
	if strings.Join(phases, ",") != "prepare" {
		t.Fatal(phases)
	}
}
