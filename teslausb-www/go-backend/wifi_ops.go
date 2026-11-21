package main

// wifi_ops.go — nmcli-based WiFi network management (NetworkManager / Bookworm+).
//
// API surface:
//   GET  /api/wifi/networks      — saved connections + connected_via_wifi flag
//   POST /api/wifi/networks      — add a new network {ssid, password}
//   DELETE /api/wifi/networks/{ssid} — remove a saved network (blocked if active)
//   GET  /api/wifi/scan          — active scan of nearby APs (takes 2–4 s)

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ── types ──────────────────────────────────────────────────────────────────────

// WifiNetwork is a saved (configured) WiFi connection.
type WifiNetwork struct {
	SSID   string `json:"ssid"`
	UUID   string `json:"uuid"`
	Active bool   `json:"active"`
}

// WifiNetworksResponse is the body of GET /api/wifi/networks.
type WifiNetworksResponse struct {
	Networks         []WifiNetwork `json:"networks"`
	ConnectedViaWifi bool          `json:"connected_via_wifi"`
}

// ScannedNetwork is a nearby access point returned by GET /api/wifi/scan.
type ScannedNetwork struct {
	SSID     string `json:"ssid"`
	Signal   int    `json:"signal"`   // 0–100 (nmcli signal-strength units)
	Security string `json:"security"` // "WPA2", "WPA3", or "Open"
	Saved    bool   `json:"saved"`    // already has a saved profile
}

// ── nmcli helpers ──────────────────────────────────────────────────────────────

// splitNmcliTerse splits one line of nmcli -t output on unescaped colons,
// producing at most n fields. Escaped colons (\:) are unescaped inside each
// field, matching nmcli's documented terse-mode escaping.
func splitNmcliTerse(line string, n int) []string {
	var parts []string
	var cur strings.Builder
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) && runes[i+1] == ':' {
			cur.WriteRune(':')
			i++ // consume the escaped colon
		} else if runes[i] == ':' && (n <= 0 || len(parts) < n-1) {
			parts = append(parts, cur.String())
			cur.Reset()
		} else {
			cur.WriteRune(runes[i])
		}
	}
	parts = append(parts, cur.String())
	return parts
}

// nmcliListWifi returns all saved WiFi connection profiles.
func nmcliListWifi() ([]WifiNetwork, error) {
	out, err := exec.Command("nmcli", "-t", "-f", "NAME,UUID,TYPE,ACTIVE", "con", "show").Output()
	if err != nil {
		return nil, fmt.Errorf("nmcli con show: %w", err)
	}
	var networks []WifiNetwork
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := splitNmcliTerse(line, 4)
		if len(parts) < 4 {
			continue
		}
		name, uuid, typ, active := parts[0], parts[1], parts[2], parts[3]
		if typ != "802-11-wireless" {
			continue
		}
		networks = append(networks, WifiNetwork{
			SSID:   name,
			UUID:   uuid,
			Active: active == "yes",
		})
	}
	return networks, nil
}

// nmcliScan triggers an active rescan and returns visible access points,
// deduplicated by SSID (strongest signal wins). savedSSIDs is used to mark
// networks that already have a saved profile.
func nmcliScan(ctx context.Context, savedSSIDs map[string]bool) ([]ScannedNetwork, error) {
	cmd := exec.CommandContext(ctx, "nmcli", "-t", "-f", "SSID,SIGNAL,SECURITY", "dev", "wifi", "list", "--rescan", "yes")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nmcli dev wifi list: %w", err)
	}

	type entry struct {
		signal   int
		security string
	}
	seen := map[string]entry{}

	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := splitNmcliTerse(line, 3)
		if len(parts) < 3 {
			continue
		}
		ssid, sigStr, sec := parts[0], parts[1], parts[2]
		if ssid == "" {
			continue // hidden SSID — not manageable via this UI
		}
		sig, _ := strconv.Atoi(sigStr)
		if e, ok := seen[ssid]; !ok || sig > e.signal {
			seen[ssid] = entry{signal: sig, security: normalizeSecurity(sec)}
		}
	}

	results := make([]ScannedNetwork, 0, len(seen))
	for ssid, e := range seen {
		results = append(results, ScannedNetwork{
			SSID:     ssid,
			Signal:   e.signal,
			Security: e.security,
			Saved:    savedSSIDs[ssid],
		})
	}
	// Sort strongest-first so the most useful networks appear at the top.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Signal > results[j].Signal
	})
	return results, nil
}

// normalizeSecurity maps nmcli's SECURITY field value to a concise label.
// nmcli outputs values like "WPA2", "WPA1 WPA2", "WPA3", or "--" for open.
func normalizeSecurity(s string) string {
	s = strings.TrimSpace(s)
	switch {
	case s == "--" || s == "":
		return "Open"
	case strings.Contains(s, "WPA3"):
		return "WPA3"
	default:
		return "WPA2"
	}
}

// nmcliAddNetwork saves a new WiFi connection profile. If password is empty
// the network is treated as open. Does not force an immediate connection —
// NetworkManager's autoconnect handles that when the AP comes into range.
func nmcliAddNetwork(ssid, password string) error {
	return withConfRW(func() error {
		args := []string{
			"con", "add", "type", "wifi",
			"con-name", ssid,
			"ssid", ssid,
			"connection.autoconnect", "yes",
		}
		if password != "" {
			args = append(args,
				"wifi-sec.key-mgmt", "wpa-psk",
				"wifi-sec.psk", password,
			)
		}
		out, err := exec.Command("nmcli", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("nmcli con add: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return nil
	})
}

// nmcliDeleteNetwork removes a connection profile by UUID.
func nmcliDeleteNetwork(uuid string) error {
	return withConfRW(func() error {
		out, err := exec.Command("nmcli", "con", "delete", uuid).CombinedOutput()
		if err != nil {
			return fmt.Errorf("nmcli con delete: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return nil
	})
}

// ── HTTP handlers ──────────────────────────────────────────────────────────────

// HandleWifiNetworks lists all saved WiFi connections.
// GET /api/wifi/networks
func HandleWifiNetworks(w http.ResponseWriter, r *http.Request) {
	networks, err := nmcliListWifi()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	if networks == nil {
		networks = []WifiNetwork{} // always return an array, never null
	}

	connectedViaWifi := false
	for _, n := range networks {
		if n.Active {
			connectedViaWifi = true
			break
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(WifiNetworksResponse{
		Networks:         networks,
		ConnectedViaWifi: connectedViaWifi,
	})
}

// HandleWifiScan triggers an active scan and returns nearby access points.
// This call blocks for 2–4 s while the driver rescans.
// GET /api/wifi/scan
func HandleWifiScan(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	saved, _ := nmcliListWifi()
	savedSSIDs := make(map[string]bool, len(saved))
	for _, n := range saved {
		savedSSIDs[n.SSID] = true
	}

	results, err := nmcliScan(ctx, savedSSIDs)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	if results == nil {
		results = []ScannedNetwork{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

// HandleWifiAdd saves a new WiFi connection profile.
// POST /api/wifi/networks   body: {"ssid":"…","password":"…"}
func HandleWifiAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SSID     string `json:"ssid"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	body.SSID = strings.TrimSpace(body.SSID)
	if body.SSID == "" {
		http.Error(w, `{"error":"ssid is required"}`, http.StatusBadRequest)
		return
	}

	// Reject duplicates — nmcli would error anyway but we give a cleaner message.
	existing, err := nmcliListWifi()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	for _, n := range existing {
		if n.SSID == body.SSID {
			http.Error(w, `{"error":"a profile for this network is already saved"}`, http.StatusConflict)
			return
		}
	}

	if err := nmcliAddNetwork(body.SSID, body.Password); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintln(w, `{"status":"added"}`)
}

// HandleWifiDelete removes a saved WiFi connection profile. Returns 409 if
// the network is currently active — deleting it would cut the connection.
// DELETE /api/wifi/networks/{ssid}
func HandleWifiDelete(w http.ResponseWriter, r *http.Request) {
	ssid := r.PathValue("ssid")
	if ssid == "" {
		http.Error(w, `{"error":"ssid is required"}`, http.StatusBadRequest)
		return
	}

	networks, err := nmcliListWifi()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	var target *WifiNetwork
	for i := range networks {
		if networks[i].SSID == ssid {
			target = &networks[i]
			break
		}
	}
	if target == nil {
		http.Error(w, `{"error":"network not found"}`, http.StatusNotFound)
		return
	}
	if target.Active {
		http.Error(w, `{"error":"cannot delete the currently active network"}`, http.StatusConflict)
		return
	}

	if err := nmcliDeleteNetwork(target.UUID); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintln(w, `{"status":"deleted"}`)
}
