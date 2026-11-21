#!/usr/bin/env python3
"""portal_server.py — open WiFi AP + captive-portal HTTP server.

Creates an open NetworkManager hotspot named "Dashvault-XXXX" (last 4 hex
digits of the wlan0 MAC address) and serves portal.html on port 80 via the
NM shared-IPv4 bridge (192.168.4.1).

All DNS queries are redirected to 192.168.4.1 by the dnsmasq config installed
by configure-provisioning.sh, so any URL the user types loads the portal.

REST API (used by portal.html JS):
  GET  /api/status      → {"ssid": "Dashvault-XXXX", "ip": "192.168.4.1",
                           "last_error": "..."}
  GET  /api/scan        → [{ssid, signal, security}, ...]
  POST /api/configure   → {webui_username, webui_password, archive_type,
                           archive_host, share_name, username?, password?,
                           wifi_ssid?, wifi_password?}
                        ← {"status": "ok"}  (then server shuts down)
"""

import http.server
import json
import os
import pathlib
import signal
import subprocess
import sys
import threading
import urllib.parse

from provisioning_config import MAX_COMMAND_BYTES, apply_request, last_error

SCRIPT_DIR = pathlib.Path(__file__).parent
PORTAL_HTML = SCRIPT_DIR / 'portal.html'
CONF_FILE = os.environ.get('SETUP_CONF_FILE', '/root/teslausb_setup_variables.conf')
SENTINEL = os.environ.get('PROVISIONING_SENTINEL', '/boot/firmware/provisioning_done')
AP_IFACE = 'wlan0'
AP_IP = '192.168.4.1'
BIND_ADDRESS = os.environ.get('PORTAL_BIND', AP_IP)
AP_PORT = int(os.environ.get('PORTAL_PORT', '80'))
CON_NAME = 'teslausb-ap'

_done = threading.Event()
_configured = threading.Event()
_configure_lock = threading.Lock()


# ── NetworkManager hotspot ────────────────────────────────────────────────────

def _mac_suffix() -> str:
    """Return last 4 hex digits of wlan0 MAC, uppercased."""
    try:
        out = subprocess.check_output(
            ['nmcli', '-t', '-f', 'GENERAL.HWADDR', 'dev', 'show', AP_IFACE],
            stderr=subprocess.DEVNULL, timeout=5,
        ).decode()
        for line in out.splitlines():
            if 'HWADDR' in line:
                mac = line.split(':', 1)[-1].strip().replace(':', '')
                return mac[-4:].upper()
    except Exception:
        pass
    return '0000'


def start_ap() -> str:
    """Create and activate the NM hotspot; return SSID."""
    ssid = f'Dashvault-{_mac_suffix()}'
    # Remove any stale connection from a previous run.
    subprocess.run(['nmcli', 'con', 'delete', CON_NAME], capture_output=True)
    subprocess.run([
        'nmcli', 'con', 'add',
        'type', 'wifi',
        'con-name', CON_NAME,
        'ifname', AP_IFACE,
        'ssid', ssid,
        '802-11-wireless.mode', 'ap',
        '802-11-wireless.band', 'bg',
        '802-11-wireless.channel', '6',
        'ipv4.addresses', f'{AP_IP}/24',
        'ipv4.method', 'shared',
        'connection.autoconnect', 'no',
    ], check=True, capture_output=True)
    subprocess.run(['nmcli', 'con', 'up', CON_NAME], check=True, capture_output=True)
    print(f'[portal] AP up: SSID={ssid}  IP={AP_IP}', flush=True)
    return ssid


def stop_ap() -> None:
    subprocess.run(['nmcli', 'con', 'down', CON_NAME], capture_output=True)
    subprocess.run(['nmcli', 'con', 'delete', CON_NAME], capture_output=True)
    print('[portal] AP stopped.', flush=True)


# ── HTTP handler ───────────────────────────────────────────────────────────────

class PortalHandler(http.server.BaseHTTPRequestHandler):
    ap_ssid: str = ''

    def log_message(self, fmt, *args):  # redirect to stdout with tag
        print(f'[portal] {self.address_string()} - {fmt % args}', flush=True)

    # ── routing ──────────────────────────────────────────────────────────────

    def do_GET(self):
        path = urllib.parse.urlparse(self.path).path
        if path == '/api/status':
            self._json({
                'ssid': self.ap_ssid,
                'ip': AP_IP,
                'last_error': last_error(pathlib.Path(SENTINEL)),
            })
        elif path == '/api/scan':
            self._handle_scan()
        else:
            # Captive-portal catch-all: serve the portal for any path.
            self._serve_html()

    def do_POST(self):
        path = urllib.parse.urlparse(self.path).path
        if path == '/api/configure':
            self._handle_configure()
        else:
            self.send_error(404)

    # ── handlers ─────────────────────────────────────────────────────────────

    def _serve_html(self):
        try:
            body = PORTAL_HTML.read_bytes()
        except FileNotFoundError:
            self.send_error(500, 'portal.html missing')
            return
        self.send_response(200)
        self.send_header('Content-Type', 'text/html; charset=utf-8')
        self.send_header('Content-Length', str(len(body)))
        # Disable caching so captive portal browsers always fetch fresh.
        self.send_header('Cache-Control', 'no-store')
        self.end_headers()
        self.wfile.write(body)

    def _handle_scan(self):
        # Honour cached scan from start.sh if available; otherwise re-scan.
        cached = os.environ.get('TESLAUSB_WIFI_SCAN')
        if cached:
            try:
                data = json.loads(cached)
                self._json(data)
                return
            except json.JSONDecodeError:
                pass
        try:
            raw = subprocess.check_output(
                [sys.executable, str(SCRIPT_DIR / 'scan_wifi.py')],
                timeout=20, stderr=subprocess.DEVNULL,
            )
            data = json.loads(raw)
        except Exception as exc:
            data = {'error': str(exc)}
        self._json(data)

    def _handle_configure(self):
        try:
            length = int(self.headers.get('Content-Length', 0))
        except ValueError:
            length = -1
        if not 0 < length <= MAX_COMMAND_BYTES:
            self._json({'error': 'Bad request size'}, 400)
            return
        if _done.is_set() or _configured.is_set():
            self._json({'error': 'This device has already been configured'}, 409)
            return
        try:
            with _configure_lock:
                if _configured.is_set():
                    self._json({'error': 'This device has already been configured'}, 409)
                    return
                apply_request(
                    json.loads(self.rfile.read(length)),
                    pathlib.Path(CONF_FILE),
                    pathlib.Path(SENTINEL),
                )
                _configured.set()
        except (ValueError, UnicodeDecodeError) as exc:
            # json.JSONDecodeError is a ValueError.
            self._json({'error': str(exc)}, 400)
            return
        except OSError as exc:
            print(f'[portal] Could not save configuration: {exc}', flush=True)
            self._json({'error': 'Could not save the configuration'}, 500)
            return

        print(f'[portal] Configuration saved; sentinel written: {SENTINEL}', flush=True)
        self._json({'status': 'ok'})
        # Give the response 1 second to flush before stopping the server.
        threading.Timer(1.0, _done.set).start()

    # ── helper ───────────────────────────────────────────────────────────────

    def _json(self, obj, status=200):
        body = json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Cache-Control', 'no-store')
        self.end_headers()
        self.wfile.write(body)


# ── main ───────────────────────────────────────────────────────────────────────

def main():
    ssid = start_ap()
    PortalHandler.ap_ssid = ssid

    try:
        server = http.server.ThreadingHTTPServer((BIND_ADDRESS, AP_PORT), PortalHandler)
    except Exception:
        stop_ap()
        raise
    server_thread = threading.Thread(target=server.serve_forever, daemon=True)
    server_thread.start()
    print(f'[portal] Captive portal running at http://{AP_IP}/ — connect to "{ssid}"',
          flush=True)

    def _shutdown(signum, frame):
        _done.set()

    signal.signal(signal.SIGTERM, _shutdown)
    signal.signal(signal.SIGINT, _shutdown)

    try:
        _done.wait()
    finally:
        print('[portal] Shutting down…', flush=True)
        server.shutdown()
        stop_ap()


if __name__ == '__main__':
    main()
