#!/usr/bin/env python3
"""ble_service.py — BLE GATT peripheral for TeslaUSB first-run provisioning.

Exposes a custom GATT service that lets a Web Bluetooth (Chrome/Edge) page:
  1. Read nearby WiFi networks (chunked, to work around 512-byte GATT limits).
  2. Send archive configuration (chunked write).
  3. Read the result of the last command.

UUIDs
─────
Service:           12340000-5678-1234-5678-1234567890ab
WIFI_SCAN  (R/N):  12340001-5678-1234-5678-1234567890ab
COMMAND    (W):    12340002-5678-1234-5678-1234567890ab
STATUS     (R/N):  12340003-5678-1234-5678-1234567890ab

Chunking protocol (WIFI_SCAN read, COMMAND write)
──────────────────────────────────────────────────
Both large payloads use the same 2-byte header:
  byte 0 : chunk index (0-based)
  byte 1 : total chunks
  bytes 2+: payload fragment (up to CHUNK_SIZE bytes)

For WIFI_SCAN the value is a flat JSON byte string of at most 500 bytes. A
client reads it with ATT long reads, which BlueZ passes here as an "offset".

For COMMAND the client sends multiple Write requests with the 2-byte header
prefix; this peripheral reassembles them and executes on receipt of the final
chunk (index == total−1).

Requirements on the Pi
──────────────────────
  apt install python3-dbus python3-gi bluez
  (BlueZ ≥ 5.54, shipped with Raspberry Pi OS Bookworm)
"""

import json
import os
import pathlib
import signal
import subprocess
import sys
import threading
import time

import dbus
import dbus.exceptions
import dbus.mainloop.glib
import dbus.service
from gi.repository import GLib

from provisioning_config import ChunkAssembler, apply_request, fit_scan_json

# ── UUIDs ─────────────────────────────────────────────────────────────────────

SVC_UUID    = '12340000-5678-1234-5678-1234567890ab'
SCAN_UUID   = '12340001-5678-1234-5678-1234567890ab'
CMD_UUID    = '12340002-5678-1234-5678-1234567890ab'
STATUS_UUID = '12340003-5678-1234-5678-1234567890ab'

# ── paths ─────────────────────────────────────────────────────────────────────

SCRIPT_DIR = pathlib.Path(__file__).parent
CONF_FILE  = os.environ.get('SETUP_CONF_FILE', '/root/teslausb_setup_variables.conf')
SENTINEL   = os.environ.get('PROVISIONING_SENTINEL', '/boot/firmware/provisioning_done')
# Stop advertising after this long, unless a client is connected and filling in
# the form. start.sh enforces a hard limit around this.
IDLE_TIMEOUT = int(os.environ.get('BLE_TIMEOUT', '90'))

# ── D-Bus / BlueZ constants ───────────────────────────────────────────────────

BLUEZ_SERVICE   = 'org.bluez'
ADAPTER_IFACE   = 'org.bluez.Adapter1'
GATT_MGR_IFACE  = 'org.bluez.GattManager1'
LE_ADV_MGR_IFACE = 'org.bluez.LEAdvertisingManager1'
LE_ADV_IFACE    = 'org.bluez.LEAdvertisement1'
GATT_SVC_IFACE  = 'org.bluez.GattService1'
GATT_CHAR_IFACE = 'org.bluez.GattCharacteristic1'
PROP_IFACE      = 'org.freedesktop.DBus.Properties'
DBUS_OM_IFACE   = 'org.freedesktop.DBus.ObjectManager'

BASE_PATH       = '/com/teslausb/provisioning'
SVC_PATH        = BASE_PATH + '/service0'
SCAN_CHAR_PATH  = SVC_PATH  + '/char0'
CMD_CHAR_PATH   = SVC_PATH  + '/char1'
STAT_CHAR_PATH  = SVC_PATH  + '/char2'
ADV_PATH        = BASE_PATH + '/advertisement0'

# ── helper: get first BT adapter ─────────────────────────────────────────────

def find_adapter(bus: dbus.SystemBus) -> dbus.ObjectPath:
    remote_om = dbus.Interface(bus.get_object(BLUEZ_SERVICE, '/'),
                               DBUS_OM_IFACE)
    objects = remote_om.GetManagedObjects()
    for path, ifaces in objects.items():
        if ADAPTER_IFACE in ifaces:
            return path
    raise RuntimeError('No Bluetooth adapter found')


# ── advertisement ──────────────────────────────────────────────────────────────

class LEAdvertisement(dbus.service.Object):
    PATH_BASE = ADV_PATH

    def __init__(self, bus: dbus.SystemBus):
        dbus.service.Object.__init__(self, bus, ADV_PATH)
        self._bus = bus

    @dbus.service.method(PROP_IFACE, in_signature='ss', out_signature='v')
    def Get(self, interface, prop):
        return self.GetAll(interface)[prop]

    @dbus.service.method(PROP_IFACE, in_signature='s', out_signature='a{sv}')
    def GetAll(self, interface):
        if interface != LE_ADV_IFACE:
            raise dbus.exceptions.DBusException(
                'org.freedesktop.DBus.Error.InvalidArgs')
        return {
            'Type':             dbus.String('peripheral'),
            'ServiceUUIDs':     dbus.Array([SVC_UUID], signature='s'),
            'LocalName':        dbus.String('Dashvault Setup'),
            'IncludeTxPower':   dbus.Boolean(True),
        }

    @dbus.service.method(LE_ADV_IFACE, in_signature='', out_signature='')
    def Release(self):
        print('[ble] Advertisement released.', flush=True)


# ── GATT application (ObjectManager) ──────────────────────────────────────────

class Application(dbus.service.Object):
    def __init__(self, bus: dbus.SystemBus, service: 'ProvisioningService'):
        dbus.service.Object.__init__(self, bus, BASE_PATH)
        self._service = service

    @dbus.service.method(DBUS_OM_IFACE, out_signature='a{oa{sa{sv}}}')
    def GetManagedObjects(self):
        result = {}
        result[SVC_PATH]       = self._service.get_properties()
        result[SCAN_CHAR_PATH] = self._service.scan_char.get_properties()
        result[CMD_CHAR_PATH]  = self._service.cmd_char.get_properties()
        result[STAT_CHAR_PATH] = self._service.status_char.get_properties()
        return result


# ── GATT service ───────────────────────────────────────────────────────────────

class ProvisioningService:
    def __init__(self, bus: dbus.SystemBus, loop: GLib.MainLoop):
        self._loop = loop
        self.scan_char   = WifiScanCharacteristic(bus)
        self.cmd_char    = CommandCharacteristic(bus, self)
        self.status_char = StatusCharacteristic(bus)

    def get_properties(self):
        return {
            GATT_SVC_IFACE: {
                'UUID':    dbus.String(SVC_UUID),
                'Primary': dbus.Boolean(True),
                'Characteristics': dbus.Array(
                    [SCAN_CHAR_PATH, CMD_CHAR_PATH, STAT_CHAR_PATH],
                    signature='o'),
            }
        }

    def on_configured(self):
        """Called by CommandCharacteristic when configuration succeeds."""
        self.status_char.set_status('ok')
        # Exit the main loop after a delay so the client can read the status.
        GLib.timeout_add(4000, self._loop.quit)


# ── GATT characteristics ───────────────────────────────────────────────────────

class _CharacteristicBase(dbus.service.Object):
    def __init__(self, bus, path, uuid, flags, service_path):
        dbus.service.Object.__init__(self, bus, path)
        self._uuid    = uuid
        self._flags   = flags
        self._svc_path = service_path
        self._notifying = False

    def get_properties(self):
        return {
            GATT_CHAR_IFACE: {
                'Service':  dbus.ObjectPath(self._svc_path),
                'UUID':     dbus.String(self._uuid),
                'Flags':    dbus.Array(self._flags, signature='s'),
            }
        }

    @dbus.service.method(PROP_IFACE, in_signature='ss', out_signature='v')
    def Get(self, interface, prop):
        return self.get_properties()[interface][prop]

    @dbus.service.method(PROP_IFACE, in_signature='s', out_signature='a{sv}')
    def GetAll(self, interface):
        return self.get_properties().get(interface, {})

    @dbus.service.signal(PROP_IFACE, signature='sa{sv}as')
    def PropertiesChanged(self, interface, changed, invalidated):
        pass


class WifiScanCharacteristic(_CharacteristicBase):
    """READ + NOTIFY — returns the WiFi scan JSON as a UTF-8 byte string.

    BlueZ handles multi-packet READ automatically via ATT offset, so we just
    return the full value and let the stack fragment it.
    """

    def __init__(self, bus):
        super().__init__(bus, SCAN_CHAR_PATH, SCAN_UUID,
                         ['read', 'notify'], SVC_PATH)
        self._value = self._build_value()

    def _build_value(self) -> bytes:
        cached = os.environ.get('TESLAUSB_WIFI_SCAN')
        if cached:
            try:
                json.loads(cached)  # validate
                return fit_scan_json(cached.encode())
            except (json.JSONDecodeError, ValueError):
                pass
        # Re-scan if no cached value.
        try:
            raw = subprocess.check_output(
                [sys.executable, str(SCRIPT_DIR / 'scan_wifi.py')],
                timeout=20, stderr=subprocess.DEVNULL,
            )
            return fit_scan_json(raw.strip())
        except Exception:
            return b'[]'

    @dbus.service.method(GATT_CHAR_IFACE, in_signature='a{sv}', out_signature='ay')
    def ReadValue(self, options):
        return dbus.Array(list(self._value[int(options.get('offset', 0)):]), signature='y')

    @dbus.service.method(GATT_CHAR_IFACE, in_signature='a{sv}', out_signature='')
    def StartNotify(self, options):
        self._notifying = True

    @dbus.service.method(GATT_CHAR_IFACE, in_signature='', out_signature='')
    def StopNotify(self):
        self._notifying = False

    def rescan(self):
        self._value = self._build_value()
        if self._notifying:
            self.PropertiesChanged(
                GATT_CHAR_IFACE,
                {'Value': dbus.Array(list(self._value), signature='y')},
                [],
            )


class CommandCharacteristic(_CharacteristicBase):
    """WRITE — receives a JSON configuration command.

    For small payloads the whole JSON fits in one write.  For larger ones the
    client prepends a 2-byte [chunk_index, total_chunks] header.  We
    reassemble and execute when the final chunk arrives.
    """

    def __init__(self, bus, service: ProvisioningService):
        super().__init__(bus, CMD_CHAR_PATH, CMD_UUID,
                         ['write', 'write-without-response'], SVC_PATH)
        self._service = service
        self._assembler = ChunkAssembler()

    @dbus.service.method(GATT_CHAR_IFACE, in_signature='aya{sv}', out_signature='')
    def WriteValue(self, value, options):
        try:
            assembled = self._assembler.add(bytes(value))
        except ValueError as e:
            self._service.status_char.set_status(f'error: {e}')
            return
        if assembled is not None:
            self._dispatch(assembled)

    def _dispatch(self, data: bytes):
        try:
            req = json.loads(data.decode())
        except (json.JSONDecodeError, UnicodeDecodeError) as e:
            print(f'[ble] Bad command JSON: {e}', flush=True)
            self._service.status_char.set_status('error: bad JSON')
            return

        cmd = req.get('type', '')
        if cmd == 'configure':
            self._service.status_char.set_status('working')
            self._do_configure(req.get('payload', {}))
        elif cmd == 'rescan':
            threading.Thread(target=self._service.scan_char.rescan,
                             daemon=True).start()
        else:
            self._service.status_char.set_status(f'error: unknown command {cmd!r}')

    def _do_configure(self, payload: dict):
        try:
            apply_request(payload, pathlib.Path(CONF_FILE), pathlib.Path(SENTINEL))
        except ValueError as e:
            self._service.status_char.set_status(f'error: {e}')
            return
        except OSError as e:
            print(f'[ble] Could not save configuration: {e}', flush=True)
            self._service.status_char.set_status('error: could not save the configuration')
            return
        print(f'[ble] Configuration saved; sentinel written: {SENTINEL}', flush=True)
        self._service.on_configured()


class StatusCharacteristic(_CharacteristicBase):
    """READ + NOTIFY — reports last command result: 'ok' or 'error: …'"""

    def __init__(self, bus):
        super().__init__(bus, STAT_CHAR_PATH, STATUS_UUID,
                         ['read', 'notify'], SVC_PATH)
        self._value = b'idle'
        self._notifying = False

    def set_status(self, msg: str):
        self._value = msg.encode()
        if self._notifying:
            self.PropertiesChanged(
                GATT_CHAR_IFACE,
                {'Value': dbus.Array(list(self._value), signature='y')},
                [],
            )
        print(f'[ble] Status: {msg}', flush=True)

    @dbus.service.method(GATT_CHAR_IFACE, in_signature='a{sv}', out_signature='ay')
    def ReadValue(self, options):
        return dbus.Array(list(self._value[int(options.get('offset', 0)):]), signature='y')

    @dbus.service.method(GATT_CHAR_IFACE, in_signature='a{sv}', out_signature='')
    def StartNotify(self, options):
        self._notifying = True

    @dbus.service.method(GATT_CHAR_IFACE, in_signature='', out_signature='')
    def StopNotify(self):
        self._notifying = False


# ── main ───────────────────────────────────────────────────────────────────────

def main():
    dbus.mainloop.glib.DBusGMainLoop(set_as_default=True)
    bus  = dbus.SystemBus()
    loop = GLib.MainLoop()

    adapter_path = find_adapter(bus)
    adapter_props = dbus.Interface(
        bus.get_object(BLUEZ_SERVICE, adapter_path), PROP_IFACE)

    # Power on and make discoverable.
    adapter_props.Set(ADAPTER_IFACE, 'Powered',      dbus.Boolean(True))
    adapter_props.Set(ADAPTER_IFACE, 'DiscoverableTimeout', dbus.UInt32(0))
    adapter_props.Set(ADAPTER_IFACE, 'Discoverable', dbus.Boolean(True))
    adapter_props.Set(ADAPTER_IFACE, 'Pairable',     dbus.Boolean(False))

    service     = ProvisioningService(bus, loop)
    app         = Application(bus, service)
    adv         = LEAdvertisement(bus)

    gatt_mgr = dbus.Interface(
        bus.get_object(BLUEZ_SERVICE, adapter_path), GATT_MGR_IFACE)
    adv_mgr = dbus.Interface(
        bus.get_object(BLUEZ_SERVICE, adapter_path), LE_ADV_MGR_IFACE)

    def _reg_app_ok():
        print('[ble] GATT application registered.', flush=True)

    def _reg_app_err(e):
        print(f'[ble] Failed to register GATT application: {e}', flush=True)
        loop.quit()

    def _reg_adv_ok():
        print('[ble] Advertisement registered.  Waiting for BLE client…', flush=True)

    def _reg_adv_err(e):
        print(f'[ble] Failed to register advertisement: {e}', flush=True)

    gatt_mgr.RegisterApplication(
        BASE_PATH, {},
        reply_handler=_reg_app_ok,
        error_handler=_reg_app_err,
    )
    adv_mgr.RegisterAdvertisement(
        ADV_PATH, {},
        reply_handler=_reg_adv_ok,
        error_handler=_reg_adv_err,
    )

    deadline = time.monotonic() + IDLE_TIMEOUT

    def _client_connected() -> bool:
        objects = dbus.Interface(bus.get_object(BLUEZ_SERVICE, '/'),
                                 DBUS_OM_IFACE).GetManagedObjects()
        return any(bool(ifaces.get('org.bluez.Device1', {}).get('Connected'))
                   for ifaces in objects.values())

    def _check_idle():
        if time.monotonic() >= deadline and not _client_connected():
            print('[ble] No client connected; stopping.', flush=True)
            loop.quit()
            return False
        return True

    GLib.timeout_add_seconds(5, _check_idle)

    def _sigterm(signum, frame):
        loop.quit()

    signal.signal(signal.SIGTERM, _sigterm)
    signal.signal(signal.SIGINT, _sigterm)

    print('[ble] BLE provisioning service started.', flush=True)
    loop.run()
    print('[ble] BLE service stopped.', flush=True)


if __name__ == '__main__':
    main()
