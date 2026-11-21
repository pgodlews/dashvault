// teslausb-backend: unified Go service.
//
// Route groups:
//
//	/cgi-bin/*                       — file and system operations (replaces bash CGI)
//	/api/status                      — JSON snapshot (uptime, CPU temp, disk, Wi-Fi, archive)
//	/api/status/stream               — SSE stream of the same payload, pushed every poll interval
//	/api/update/*                    — OTA update management
//	/api/config/snapshot-interval    — read/write snapshot interval
//	/api/wifi/*                      — WiFi network management (NetworkManager / Bookworm+)
//	/api/setup/*                     — first-run setup wizard
//	/api/benchmark/*                 — SD card benchmark, write tracking, TBW threshold
//
// Supports systemd socket activation: when LISTEN_FDS=1 is set fd 3 is used
// as the pre-bound socket; otherwise the server binds to -addr (or $PORT).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// version is injected at build time via -ldflags "-X main.version=vX.Y.Z".
// The zero value "dev" is used for local/development builds.
var version = "dev"

func main() {
	addr := flag.String("addr", listenAddr(), "listen address (ignored when socket-activated)")
	poll := flag.Duration("poll", 2*time.Second, "status metrics collection interval")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Old OTA installers copy bin/ but do not install service units. The new
	// runtime finishes that migration after the installer releases its lock.
	if _, err := os.Stat("/root/bin/ota_finalize.sh"); err == nil {
		if output, err := exec.Command("/root/bin/ota_finalize.sh").CombinedOutput(); err != nil {
			logger.Error("OTA unit migration failed", "err", err, "output", string(output))
		}
	}

	col := NewCollector()
	broker := NewBroker(col, *poll)

	ln, err := listen(*addr)
	if err != nil {
		logger.Error("listen failed", "err", err)
		os.Exit(1)
	}
	logger.Info("teslausb-backend ready", "addr", ln.Addr(), "poll", poll.String())

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	go broker.Run(ctx)
	go NewClipWatcher(logger).Run(ctx)

	srv := &http.Server{
		Handler:      newHTTPHandler(broker, cachedVerifier(verifyWebUICredentials, func() string { return webUIAuthFile })),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // SSE streams are long-lived
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		logger.Info("shutting down")
		srv.Close()
	}()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		logger.Error("serve failed", "err", err)
		os.Exit(1)
	}
}

// newHTTPHandler puts authentication outside the entire route tree, including
// read-only downloads, streams and setup. CSRF protects mutations separately.
func newHTTPHandler(broker *Broker, verify credentialVerifier) http.HandlerFunc {
	mux := http.NewServeMux()

	// Read-only endpoints: issue the CSRF cookie so the UI can echo it back
	// as X-CSRF-Token on subsequent mutating requests.
	read := issueCSRFCookie
	// Mutating endpoints: require matching X-CSRF-Token header, then log to the
	// audit trail. requireCSRF also issues the cookie so a first-time client
	// gets one in the same response.
	mutate := func(h http.HandlerFunc) http.HandlerFunc {
		return requireCSRF(issueCSRFCookie(auditMiddleware(h)))
	}

	// File operations
	mux.HandleFunc("/cgi-bin/ls.sh", read(HandleLs))
	mux.HandleFunc("/cgi-bin/download.sh", read(HandleDownload))
	mux.HandleFunc("/cgi-bin/downloadzip.sh", read(HandleDownloadZip))
	mux.HandleFunc("POST /cgi-bin/upload.sh", mutate(HandleUpload))
	mux.HandleFunc("POST /cgi-bin/rm.sh", mutate(HandleRm))
	mux.HandleFunc("POST /cgi-bin/mkdir.sh", mutate(HandleMkdir))
	mux.HandleFunc("POST /cgi-bin/cp.sh", mutate(HandleCp))
	mux.HandleFunc("POST /cgi-bin/mv.sh", mutate(HandleMv))

	// System operations
	mux.HandleFunc("POST /cgi-bin/reboot.sh", mutate(HandleReboot))
	mux.HandleFunc("POST /cgi-bin/toggledrives.sh", mutate(HandleToggleDrives))
	mux.HandleFunc("POST /cgi-bin/trigger_sync.sh", mutate(HandleTriggerSync))
	mux.HandleFunc("/cgi-bin/randomdata.sh", read(HandleRandomData))
	mux.HandleFunc("/api/config/snapshot-interval", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			read(HandleSnapshotInterval)(w, r)
		} else {
			mutate(HandleSnapshotInterval)(w, r)
		}
	})

	// Archive backend config
	mux.HandleFunc("/api/config/archive", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			read(HandleArchiveConfig)(w, r)
		} else {
			mutate(HandleArchiveConfig)(w, r)
		}
	})

	// Telegram bot config
	mux.HandleFunc("/api/config/telegram", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			read(HandleTelegramConfig)(w, r)
		} else {
			mutate(HandleTelegramConfig)(w, r)
		}
	})
	mux.HandleFunc("POST /api/config/telegram/test", mutate(HandleTelegramTest))

	// rsync SSH key management
	mux.HandleFunc("GET /api/config/rsync/ssh-key", read(HandleRsyncSSHKey))
	mux.HandleFunc("POST /api/config/rsync/keygen", mutate(HandleRsyncKeygen))
	mux.HandleFunc("POST /api/config/rsync/import-key", mutate(HandleRsyncImportKey))

	// WiFi management endpoints (NetworkManager / Bookworm+)
	mux.HandleFunc("GET /api/wifi/networks", read(HandleWifiNetworks))
	mux.HandleFunc("POST /api/wifi/networks", mutate(HandleWifiAdd))
	mux.HandleFunc("DELETE /api/wifi/networks/{ssid}", mutate(HandleWifiDelete))
	mux.HandleFunc("GET /api/wifi/scan", read(HandleWifiScan))

	// First-run setup wizard
	mux.HandleFunc("GET /api/setup/status", read(HandleSetupStatus))
	mux.HandleFunc("POST /api/setup/apply", mutate(HandleSetupApply))

	// SD card benchmark
	mux.HandleFunc("GET /api/benchmark/status", read(HandleBenchmarkStatus))
	mux.HandleFunc("POST /api/benchmark/run", mutate(HandleBenchmarkRun))
	mux.HandleFunc("GET /api/benchmark/threshold", read(HandleBenchmarkThreshold))
	mux.HandleFunc("POST /api/benchmark/threshold", mutate(HandleBenchmarkThreshold))

	// GPS track history and per-clip playback telemetry
	mux.HandleFunc("GET /api/telemetry/tracks", read(HandleTelemetryTracks))
	mux.HandleFunc("GET /api/telemetry/clip", read(HandleTelemetryClip))
	mux.HandleFunc("GET /api/telemetry/days", read(HandleTelemetryDays))
	// Drives near a point and camera stills (map Look around, Telegram /near)
	mux.HandleFunc("GET /api/street/near", read(HandleStreetNear))
	mux.HandleFunc("GET /api/street/frame", read(HandleStreetFrame))
	mux.HandleFunc("POST /api/street/install-ffmpeg", mutate(HandleStreetInstallFFmpeg))
	mux.HandleFunc("GET /api/telemetry/settings", read(HandleTelemetrySettings))
	mux.HandleFunc("POST /api/telemetry/settings", mutate(HandleTelemetrySettings))
	mux.HandleFunc("POST /api/telemetry/clear", mutate(HandleTelemetryClear))

	// Power profiles, dynamic thermal soft-throttling, and temperature history
	mux.HandleFunc("GET /api/power/config", read(HandlePowerConfig))
	mux.HandleFunc("POST /api/power/config", mutate(HandlePowerConfig))
	mux.HandleFunc("GET /api/power/history", read(HandlePowerHistory))
	mux.HandleFunc("POST /api/power/history/clear", mutate(HandlePowerHistoryClear))

	// OTA update endpoints
	mux.HandleFunc("GET /api/update/status", read(HandleUpdateStatus))
	mux.HandleFunc("POST /api/update/check", mutate(HandleUpdateCheck))
	mux.HandleFunc("POST /api/update/apply", mutate(HandleUpdateApply))
	mux.HandleFunc("POST /api/update/rollback", mutate(HandleUpdateRollback))
	mux.HandleFunc("GET /api/update/log", read(HandleUpdateLog))

	// Status API
	mux.HandleFunc("/api/status", read(broker.ServeSnapshot))
	mux.HandleFunc("/api/status/stream", read(broker.ServeSSE))

	// Log API
	mux.HandleFunc("/api/logs", read(HandleLogSnapshot))
	mux.HandleFunc("/api/logs/stream", read(HandleLogStream))

	routes := authRateLimit(requireBasicAuth(mux.ServeHTTP, verify))
	// nginx asks the gate about every request before serving it; the gate does
	// its own failure accounting, so it sits outside the route chain.
	gate := authGate(verify)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authGatePath {
			gate(w, r)
			return
		}
		routes(w, r)
	}
}

// listenAddr returns the default listen address, honouring $PORT.
func listenAddr() string {
	if p := os.Getenv("PORT"); p != "" {
		return "127.0.0.1:" + p
	}
	return "127.0.0.1:8080"
}

// listen returns a socket-activated listener when LISTEN_FDS≥1 (systemd
// socket activation), otherwise binds to addr.
func listen(addr string) (net.Listener, error) {
	if fds := os.Getenv("LISTEN_FDS"); fds != "" {
		n, err := strconv.Atoi(fds)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid LISTEN_FDS=%q", fds)
		}
		f := os.NewFile(3, "socket")
		if f == nil {
			return nil, fmt.Errorf("fd 3 unavailable for socket activation")
		}
		return net.FileListener(f)
	}
	return net.Listen("tcp", addr)
}
