import importlib
import subprocess
import sys
from pathlib import Path
from typing import Any

import pytest

ROOT = Path(__file__).resolve().parents[2]
PROVISIONING_DIR = ROOT / "run/provisioning"


@pytest.fixture()
def provisioning_modules() -> Any:
    sys.path.insert(0, str(PROPER := PROVISIONING_DIR))
    try:
        config = importlib.import_module("provisioning_config")
        portal = importlib.import_module("portal_server")
        yield config, portal
    finally:
        sys.path.remove(str(PROPER))


@pytest.mark.parametrize(
    ("archive_type", "expected"),
    [
        (
            "cifs",
            {
                "ARCHIVE_SERVER": "nas.local",
                "SHARE_NAME": "tesla/dashcam",
                "SHARE_USER": "tesla",
                "SHARE_PASSWORD": "pa$word's",
            },
        ),
        (
            "nfs",
            {
                "ARCHIVE_SERVER": "nas.local",
                "SHARE_NAME": "/volume1/tesla",
                "NFS_SERVER": "nas.local",
                "NFS_PATH": "/volume1/tesla",
            },
        ),
        (
            "rsync",
            {
                "RSYNC_SERVER": "nas.local",
                "RSYNC_PATH": "/volume1/tesla",
                "RSYNC_USER": "tesla",
            },
        ),
        (
            "rclone",
            {"RCLONE_DRIVE": "nas.local", "RCLONE_PATH": "tesla/dashcam"},
        ),
    ],
)
def test_provisioning_configuration_uses_runtime_keys(
    provisioning_modules: Any,
    tmp_path: Path,
    archive_type: str,
    expected: dict[str, str],
) -> None:
    config, _ = provisioning_modules
    path = tmp_path / "teslausb_setup_variables.conf"

    config.write_configuration(
        path,
        archive_type,
        "nas.local",
        expected.get("SHARE_NAME", expected.get("RSYNC_PATH", "tesla/dashcam")),
        "tesla",
        "pa$word's",
    )

    saved = config.read_configuration(path)
    assert saved["ARCHIVE_SYSTEM"] == archive_type
    for key, value in expected.items():
        assert saved[key] == value
    assert "ARCHIVE_HOST" not in saved
    assert path.stat().st_mode & 0o777 == 0o600


def test_none_provisioning_needs_no_archive_server(
    provisioning_modules: Any, tmp_path: Path
) -> None:
    config, _ = provisioning_modules
    path = tmp_path / "config"
    config.write_configuration(path, "none", "", "", "", "")
    assert config.read_configuration(path) == {"ARCHIVE_SYSTEM": "none"}


@pytest.mark.parametrize(
    ("archive_type", "host", "share", "user", "password"),
    [
        ("rsync", "nas", "/x", "-oProxyCommand=id", ""),
        ("rsync", "nas", "/x", "", ""),
        ("rsync", "-nas", "/x", "pi", ""),
        ("rclone", "--config=/tmp/x", "p", "", ""),
        ("cifs", "nas", "cam\nexport X=1", "pi", "pw"),
        ("cifs", "nas", "cam", "pi", "pw\rmore"),
        ("cifs", "nas host", "cam", "pi", "pw"),
        ("nfs", "nas", "/a\u2028b", "", ""),
    ],
)
def test_configuration_rejects_option_and_line_injection(
    provisioning_modules: Any,
    tmp_path: Path,
    archive_type: str,
    host: str,
    share: str,
    user: str,
    password: str,
) -> None:
    config, _ = provisioning_modules
    path = tmp_path / "config"
    with pytest.raises(ValueError):
        config.write_configuration(path, archive_type, host, share, user, password)
    assert not path.exists()


def test_portal_and_ble_share_one_configuration_writer() -> None:
    for name in ("portal_server.py", "ble_service.py"):
        source = (PROVISIONING_DIR / name).read_text()
        assert "apply_request(" in source
        assert "nmcli', 'con', 'add', 'type', 'wifi',\n            'con-name', ssid" not in source
        assert "export ARCHIVE_HOST" not in source


def test_ap_profile_assigns_portal_address(provisioning_modules: Any, monkeypatch: Any) -> None:
    _, portal = provisioning_modules
    calls: list[list[str]] = []

    def run(arguments: list[str], **_: Any) -> None:
        calls.append(arguments)

    monkeypatch.setattr(portal, "_mac_suffix", lambda: "ABCD")
    monkeypatch.setattr(portal.subprocess, "run", run)
    assert portal.start_ap() == "Dashvault-ABCD"
    add = next(call for call in calls if "add" in call)
    address_index = add.index("ipv4.addresses")
    assert add[address_index + 1] == "192.168.4.1/24"
    assert add[add.index("ipv4.method") + 1] == "shared"


def test_ap_is_removed_when_http_bind_fails(provisioning_modules: Any, monkeypatch: Any) -> None:
    _, portal = provisioning_modules
    stopped = False
    monkeypatch.setattr(portal, "start_ap", lambda: "Dashvault-ABCD")

    def stop() -> None:
        nonlocal stopped
        stopped = True

    monkeypatch.setattr(portal, "stop_ap", stop)
    monkeypatch.setattr(
        portal.http.server,
        "ThreadingHTTPServer",
        lambda *_: (_ for _ in ()).throw(OSError("address unavailable")),
    )
    with pytest.raises(OSError, match="address unavailable"):
        portal.main()
    assert stopped


def test_provisioning_gate_recognizes_written_archive_keys() -> None:
    import re

    source = (PROVISIONING_DIR / "start.sh").read_text()
    match = re.search(r"grep -qE '([^']+)' \"\$CONF_FILE\"", source)
    assert match is not None
    pattern = re.compile(match.group(1))
    assert pattern.search("export ARCHIVE_SYSTEM='rsync'\n")
    assert pattern.search("export RSYNC_SERVER='nas'\n")
    assert pattern.search("export ARCHIVE_SERVER='nas'\n")
    assert pattern.search("export RCLONE_DRIVE='gdrive'\n")
    assert pattern.search("export ARCHIVE_HOST='nas'\n") is None
    assert "ARCHIVE_HOST=" not in source.split("starting provisioning", 1)[0]


def test_successful_provisioning_reboots_into_normal_setup() -> None:
    source = (PROVISIONING_DIR / "start.sh").read_text()
    after = source[source.index("# ── post-provisioning") :]
    assert "systemctl reboot" in after
    # On a fresh image setup-teslausb only exists after rc.local has fetched it.
    assert "setup-teslausb" not in after
    assert "teslausb-configure.service" not in source


def test_portal_allows_none_archive_type(
    provisioning_modules: Any, tmp_path: Path, monkeypatch: Any
) -> None:
    from io import BytesIO

    config, portal = provisioning_modules
    conf_path = tmp_path / "conf"
    sentinel_path = tmp_path / "provisioning_done"
    monkeypatch.setattr(portal, "CONF_FILE", str(conf_path))
    monkeypatch.setattr(portal, "SENTINEL", str(sentinel_path))
    monkeypatch.setattr(
        portal.threading, "Timer", lambda *_: type("T", (), {"start": lambda self: None})()
    )

    monkeypatch.setattr(portal, "_configured", portal.threading.Event())
    payload = (
        b'{"archive_type": "none", "archive_host": "", "share_name": "",'
        b' "webui_username": "admin", "webui_password": "long enough"}'
    )
    handler = portal.PortalHandler.__new__(portal.PortalHandler)
    handler.headers = {"Content-Length": str(len(payload))}
    handler.rfile = BytesIO(payload)
    handler.wfile = BytesIO()
    responses: list[int] = []
    handler.send_response = lambda code, *_: responses.append(code)
    handler.send_header = lambda *_: None
    handler.end_headers = lambda: None
    handler.send_error = lambda code, *_: responses.append(code)

    handler._handle_configure()
    assert responses == [200]
    assert config.read_configuration(conf_path) == {
        "ARCHIVE_SYSTEM": "none",
        "WEBUI_USERNAME": "admin",
        "WEBUI_PASSWORD": "long enough",
        "PI_PASSWORD": "long enough",
    }
    assert sentinel_path.exists()


VALID_REQUEST = {
    "archive_type": "none",
    "webui_username": "admin",
    "webui_password": "long enough",
    "wifi_ssid": "Home Wi-Fi",
    "wifi_password": "pa$s'word 1",
}


def test_request_becomes_a_config_the_shell_can_source(
    provisioning_modules: Any, tmp_path: Path
) -> None:
    config, _ = provisioning_modules
    path, sentinel = tmp_path / "conf", tmp_path / "provisioning_done"
    (tmp_path / config.ERROR_FILE_NAME).write_text("old failure")
    config.apply_request(VALID_REQUEST, path, sentinel)
    assert sentinel.exists() and not (tmp_path / config.ERROR_FILE_NAME).exists()
    result = subprocess.run(  # noqa: S603
        [
            "/bin/bash",
            "-euc",
            f'source "{path}"; printf "%s|%s|%s" "$SSID" "$WIFIPASS" "$PI_PASSWORD"',
        ],
        capture_output=True,
        text=True,
        check=True,
    )
    assert result.stdout == "Home Wi-Fi|pa$s'word 1|long enough"


@pytest.mark.parametrize(
    "change",
    [
        {"webui_password": ""},
        {"webui_password": "short"},
        {"webui_username": ""},
        {"webui_username": "-admin"},
        {"webui_username": "a:b"},
        {"webui_password": "long enough\nexport X=1"},
        {"wifi_password": "short"},
        {"wifi_password": "x" * 64},
        {"wifi_ssid": "x" * 33},
        {"wifi_ssid": "home\nexport X=1"},
        {"archive_type": "ftp"},
        {"archive_type": "rsync", "archive_host": "nas", "share_name": "/x", "username": "-oX"},
        {"wifi_ssid": 7},
    ],
)
def test_bad_requests_write_nothing(
    provisioning_modules: Any, tmp_path: Path, change: dict[str, Any]
) -> None:
    config, _ = provisioning_modules
    path, sentinel = tmp_path / "conf", tmp_path / "provisioning_done"
    with pytest.raises(ValueError):
        config.apply_request({**VALID_REQUEST, **change}, path, sentinel)
    assert not path.exists() and not sentinel.exists()


def test_open_network_and_hex_key_are_accepted(provisioning_modules: Any, tmp_path: Path) -> None:
    config, _ = provisioning_modules
    for key in ("", "ab" * 32):
        config.apply_request(
            {**VALID_REQUEST, "wifi_password": key}, tmp_path / "conf", tmp_path / "done"
        )
        assert config.read_configuration(tmp_path / "conf")["WIFIPASS"] == key


def test_scan_value_fits_one_gatt_attribute(provisioning_modules: Any) -> None:
    import json

    config, _ = provisioning_modules
    networks = [{"ssid": f"Network {i}", "signal": 90 - i, "security": "WPA2"} for i in range(40)]
    value = config.fit_scan_json(json.dumps(networks).encode())
    kept = json.loads(value)
    assert len(value) <= 500 and 0 < len(kept) < 40
    assert kept == networks[: len(kept)]  # strongest first
    assert config.fit_scan_json(b"not json") == b"[]"


def test_chunks_reassemble_and_bad_headers_are_rejected(provisioning_modules: Any) -> None:
    config, _ = provisioning_modules
    assembler = config.ChunkAssembler()
    assert assembler.add(bytes([0, 3]) + b"ab") is None
    assert assembler.add(bytes([2, 3]) + b"ef") is None
    assert assembler.add(bytes([1, 3]) + b"cd") == b"abcdef"
    # An abandoned message does not leak into the next one.
    assert assembler.add(bytes([0, 2]) + b"xx") is None
    assert assembler.add(bytes([0, 1]) + b"new") == b"new"
    for bad in (b"", b"\x00", bytes([0, 0]), bytes([3, 3])):
        with pytest.raises(ValueError):
            assembler.add(bad)
    with pytest.raises(ValueError):
        assembler.add(bytes([0, 2]) + b"x" * (config.MAX_COMMAND_BYTES + 1))


def test_first_boot_starts_provisioning_only_without_a_config() -> None:
    rc_local = (ROOT / "pi-gen-sources/00-teslausb-tweaks/files/rc.local").read_text()
    moved = rc_local.index("mv /teslausb/teslausb_setup_variables.conf /root/")
    gate = rc_local.index("waiting for setup over the hotspot or Bluetooth")
    assert moved < gate < rc_local.index("\nenable_wifi\n")
    block = rc_local[
        rc_local.rindex("if [ ! -e /root/teslausb_setup_variables.conf ]", 0, gate) : gate
    ]
    assert "TESLAUSB_SETUP_FINISHED" in block and "/root/provisioning/start.sh" in block

    image = (ROOT / "pi-gen-sources/00-teslausb-tweaks/00-run.sh").read_text()
    assert "boot/firmware/teslausb_setup_variables.conf.sample" in image
    assert 'boot/firmware/teslausb_setup_variables.conf"' not in image
    assert "systemctl enable teslausb-provisioning" not in image


def test_wrong_wifi_password_returns_to_provisioning_only_before_setup_finished() -> None:
    rc_local = (ROOT / "pi-gen-sources/00-teslausb-tweaks/files/rc.local").read_text()
    start = rc_local.index('setup_progress "Failed to connect to wifi"')
    block = rc_local[start : rc_local.index('setup_progress "Configuring wpa_supplicant"')]
    assert "/teslausb/provisioning_done" in block
    assert "! -e /teslausb/TESLAUSB_SETUP_FINISHED" in block
    assert "rm -f /root/teslausb_setup_variables.conf /teslausb/provisioning_done" in block
    assert "teslausb-provisioning.service" in block


def test_portal_frees_port_80_and_image_has_hotspot_packages() -> None:
    start = (PROVISIONING_DIR / "start.sh").read_text()
    assert start.index("systemctl stop nginx") < start.index("run_ap() {")
    packages = (ROOT / "pi-gen-sources/00-teslausb-tweaks/00-packages").read_text().split()
    for package in ("dnsmasq-base", "nftables", "python3-dbus", "python3-gi"):
        assert package in packages


def test_bluetooth_runs_without_client_plugins_only_until_reboot() -> None:
    start = (PROVISIONING_DIR / "start.sh").read_text()
    assert "--noplugin=*" in start
    assert "override=/run/systemd/system/bluetooth.service.d/" in start
    assert "ExecStart=\\nExecStart=" in start
    assert start.index("bluetooth_server_only || true") < start.index("run_ble() {")


def test_captive_dns_is_restored_when_provisioning_runs_again() -> None:
    start = (PROVISIONING_DIR / "start.sh").read_text()
    written = start.index("""echo 'address=/#/192.168.4.1' > "$CAPTIVE_DNS\"""")
    assert written < start.index("run_ap() {") < start.index('rm -f "$CAPTIVE_DNS"')


NMCLI_STUB = r"""#!/bin/bash
echo "$*" >> "$NM_LOG"
case "$*" in
  "-g UUID,TYPE connection show") cat "$NM_PROFILES" 2>/dev/null ;;
  *"802-11-wireless.ssid connection show"*) echo "$NM_PROFILE_SSID" ;;
  "connection delete uuid "*) : > "$NM_PROFILES" ;;
  "-t --escape no -f SSID device wifi list") printf '%b' "$NM_VISIBLE" ;;
  *"device wifi connect"*)
    if [ "$NM_CONNECT" = fail ]; then echo "Error: Secrets were required, but not provided."; exit 4; fi ;;
esac
exit 0
"""


def run_wifi_join(tmp_path: Path, **env: str) -> tuple[int, list[str], str]:
    import os
    import re
    import shutil

    bash = "/opt/homebrew/bin/bash" if Path("/opt/homebrew/bin/bash").exists() else "bash"
    rc_local = (ROOT / "pi-gen-sources/00-teslausb-tweaks/files/rc.local").read_text()
    functions = re.search(
        r"^function forget_wifi \(\).*?(?=^function enable_wifi)",
        rc_local,
        re.DOTALL | re.MULTILINE,
    )
    assert functions
    commands = tmp_path / "bin"
    commands.mkdir()
    (commands / "nmcli").write_text(NMCLI_STUB)
    (commands / "nmcli").chmod(0o755)
    for name in ("sleep", "rfkill"):
        (commands / name).write_text("#!/bin/bash\nexit 0\n")
        (commands / name).chmod(0o755)
    log, profiles = tmp_path / "nmcli.log", tmp_path / "profiles"
    script = (
        'setup_progress() { echo "$1"; }\nSSID="Home: Net"\nWIFIPASS="sup3r-secret"\n'
        + functions.group(0)
        + "\nnm_connect_wifi"
    )
    assert shutil.which("sed")
    result = subprocess.run(  # noqa: S603
        [bash, "-e", "-c", script],
        env={
            **os.environ,
            "PATH": f"{commands}:{os.environ['PATH']}",
            "NM_LOG": str(log),
            "NM_PROFILES": str(profiles),
            "NM_PROFILE_SSID": "Home: Net",
            "NM_VISIBLE": "Home: Net\\n",
            "NM_CONNECT": "ok",
            **env,
        },
        capture_output=True,
        text=True,
        check=False,
    )
    return result.returncode, log.read_text().splitlines(), result.stdout + result.stderr


def test_wifi_join_uses_scan_and_clears_a_stale_profile(tmp_path: Path) -> None:
    (tmp_path / "profiles").write_text("aaaa-bbbb:802-11-wireless\n")
    code, calls, output = run_wifi_join(tmp_path)
    assert code == 0, output
    connect = next(call for call in calls if "device wifi connect" in call)
    assert "connection delete uuid aaaa-bbbb" in calls
    assert calls.index("connection delete uuid aaaa-bbbb") < calls.index(connect)
    assert "hidden" not in connect and "password sup3r-secret" in connect


def test_wifi_join_treats_an_unseen_network_as_hidden(tmp_path: Path) -> None:
    code, calls, output = run_wifi_join(tmp_path, NM_VISIBLE="Other\\n")
    assert code == 0, output
    assert next(call for call in calls if "device wifi connect" in call).endswith("hidden yes")


def test_wifi_join_logs_why_it_failed_without_the_password(tmp_path: Path) -> None:
    code, calls, output = run_wifi_join(tmp_path, NM_CONNECT="fail")
    assert code != 0
    assert sum("device wifi connect" in call for call in calls) == 8
    assert "wifi attempt 1 failed: Error: Secrets were required" in output
    assert "sup3r-secret" not in output


def test_default_login_is_not_exposed_during_provisioning() -> None:
    start = (PROVISIONING_DIR / "start.sh").read_text()
    stopped = start.index("systemctl stop ssh.socket ssh.service")
    assert stopped < start.index("run_ap() {") and stopped < start.index("run_ble() {")
    assert "refusing to open the setup hotspot" in start


def test_password_is_replaced_before_joining_wifi() -> None:
    rc_local = (ROOT / "pi-gen-sources/00-teslausb-tweaks/files/rc.local").read_text()
    changed = rc_local.index('"$PI_PASSWORD" | chpasswd')
    assert rc_local.index("safesource /root/teslausb_setup_variables.conf") < changed
    assert changed < rc_local.index("\nenable_wifi\n")
    block = rc_local[rc_local.index("# Replace the image's well-known password") : changed]
    assert "TESLAUSB_SETUP_FINISHED" in block


def test_install_progress_only_publishes_fixed_text() -> None:
    import re

    setup = (ROOT / "setup/pi/setup-teslausb").read_text()
    calls = re.findall(r'^setup_status (\S+) "([^"]*)"$', setup, re.MULTILINE)
    assert [int(step) for step, _ in calls] == list(range(1, 8))
    assert not any("$" in text or "`" in text for _, text in calls)
    assert setup.count("\nsetup_status ") == len(calls)
    assert setup.index('setup_status 7 "') < setup.index("copy_script setup/pi/configure-web.sh")


def test_image_shows_the_progress_page_until_setup_replaces_it() -> None:
    image = (ROOT / "pi-gen-sources/00-teslausb-tweaks/00-run.sh").read_text()
    assert 'files/installing.html "${ROOTFS_DIR}/var/www/html/index.html"' in image
    page = (ROOT / "pi-gen-sources/00-teslausb-tweaks/files/installing.html").read_text()
    assert "credentials: 'omit'" in page
    assert "src=" not in page and "https://" not in page


def test_web_app_and_portal_show_the_dashvault_name() -> None:
    pages = [PROVISIONING_DIR / "portal.html", *(ROOT / "teslausb-www/new/src").rglob("*.tsx")]
    for page in pages:
        text = page.read_text()
        assert "TeslaUSB" not in text, page
        assert "Tesla<span" not in text, page


def test_setup_defaults_to_local_storage(provisioning_modules: Any, tmp_path: Path) -> None:
    config, _ = provisioning_modules
    request = {key: value for key, value in VALID_REQUEST.items() if key != "archive_type"}
    config.apply_request(request, tmp_path / "conf", tmp_path / "done")
    assert config.read_configuration(tmp_path / "conf")["ARCHIVE_SYSTEM"] == "none"
    page = (PROVISIONING_DIR / "portal.html").read_text()
    assert '<option value="none" selected>' in page
    assert page.index('value="none"') < page.index('value="cifs"')


def test_run_once_is_retired_on_a_read_only_boot_partition() -> None:
    rc_local = (ROOT / "pi-gen-sources/00-teslausb-tweaks/files/rc.local").read_text()
    block = rc_local[
        rc_local.index("if [ -f /teslausb/run_once ]") : rc_local.index("SETUP_LOGFILE=")
    ]
    assert block.index('remount,rw "$boot_mount"') < block.index("mv /teslausb/run_once")
    assert block.index("mv /teslausb/run_once") < block.index('remount,ro "$boot_mount"')


def test_failed_wifi_at_boot_leaves_a_log() -> None:
    rc_local = (ROOT / "pi-gen-sources/00-teslausb-tweaks/files/rc.local").read_text()
    block = rc_local[rc_local.index("# The system journal is lost at power-off") :]
    assert "/mutable/wifi-boot-failure.log" in block
    assert "journalctl -b -u NetworkManager" in block
    assert "teslausb_setup_variables" not in block and "system-connections" not in block
    assert block.index("< /dev/null &") < block.index("exit 0")
