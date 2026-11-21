package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func HandleReboot(w http.ResponseWriter, r *http.Request) {
	go func() {
		time.Sleep(2 * time.Second)
		exec.Command("sudo", "reboot").Run()
	}()
	fmt.Fprintln(w, "Rebooting")
}

func HandleTriggerSync(w http.ResponseWriter, r *http.Request) {
	f, _ := os.Create("/tmp/archive_is_unreachable")
	if f != nil {
		f.Close()
	}
	fmt.Fprintln(w, "Sync triggered")
}

func HandleToggleDrives(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat("/sys/kernel/config/usb_gadget/teslausb"); err == nil {
		exec.Command("sudo", "/root/bin/disable_gadget.sh").Run()
	} else {
		exec.Command("sudo", "/root/bin/enable_gadget.sh").Run()
	}
	fmt.Fprintln(w, "Toggling drives")
}

func HandleRandomData(w http.ResponseWriter, r *http.Request) {
	out, err := exec.Command("sudo", "/root/bin/randomdata.sh").Output()
	if err == nil {
		fmt.Fprint(w, string(out))
	}
}

// confFilePath is the path to the teslausb setup variables config.
// It is a variable (not a constant) so tests can redirect it to a temp file.
var confFilePath = "/root/teslausb_setup_variables.conf"

const defaultSnapshotInterval = 480

// HandleSnapshotInterval reads (GET) or writes (POST) the SNAPSHOT_INTERVAL
// value in the setup config file.
//
// GET  → {"interval": <seconds>}
// POST → body {"interval": <seconds>}  →  {"interval": <seconds>}
func HandleSnapshotInterval(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		interval := readSnapshotInterval()
		_ = json.NewEncoder(w).Encode(map[string]int{"interval": interval})
		return
	}

	// POST: parse body and write
	var body struct {
		Interval int `json:"interval"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Interval < 60 {
		http.Error(w, `{"error":"interval must be an integer >= 60"}`, http.StatusBadRequest)
		return
	}
	if err := writeSnapshotInterval(body.Interval); err != nil {
		http.Error(w, `{"error":"failed to write config"}`, http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]int{"interval": body.Interval})
}

// readSnapshotInterval returns the current SNAPSHOT_INTERVAL from the conf
// file, or the compiled-in default if the file is absent or the key is unset.
func readSnapshotInterval() int {
	data, err := os.ReadFile(confFilePath)
	if err != nil {
		return defaultSnapshotInterval
	}
	re := regexp.MustCompile(`(?m)^export SNAPSHOT_INTERVAL=(\d+)`)
	m := re.FindSubmatch(data)
	if m == nil {
		return defaultSnapshotInterval
	}
	v, err := strconv.Atoi(string(m[1]))
	if err != nil || v < 60 {
		return defaultSnapshotInterval
	}
	return v
}

// writeSnapshotInterval upserts `export SNAPSHOT_INTERVAL=<v>` in the conf
// file.  If the key already exists (commented or not) it is replaced;
// otherwise it is appended.  The conf file lives on the read-only root
// filesystem, so the write happens inside withConfRW.
func writeSnapshotInterval(v int) error {
	return withConfRW(func() error {
		line := fmt.Sprintf("export SNAPSHOT_INTERVAL=%d", v)

		data, err := os.ReadFile(confFilePath)
		if err != nil && !os.IsNotExist(err) {
			return err
		}

		content := string(data)
		re := regexp.MustCompile(`(?m)^#?\s*export SNAPSHOT_INTERVAL=.*$`)
		if re.MatchString(content) {
			content = re.ReplaceAllString(content, line)
		} else {
			content = strings.TrimRight(content, "\n") + "\n" + line + "\n"
		}

		return writeFileAtomic(confFilePath, []byte(content), 0o600)
	})
}
