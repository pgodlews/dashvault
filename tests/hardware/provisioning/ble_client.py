#!/usr/bin/env python3
"""Talks to the Dashvault Bluetooth setup service the way the web page does, over BlueZ.

usage: ble_client.py read [WAIT_SECONDS]
       ble_client.py configure REQUEST.json [WAIT_SECONDS]

BLE_CLIENT_ADAPTER picks the local Bluetooth adapter (default hci0). Needs python3-dbus.
"""

from __future__ import annotations

import contextlib
import json
import os
import sys
import time

import dbus

BLUEZ = "org.bluez"
SERVICE, SCAN, COMMAND, STATUS = (f"1234000{i}-5678-1234-5678-1234567890ab" for i in range(4))
OBJECT_MANAGER = "org.freedesktop.DBus.ObjectManager"
PROPERTIES = "org.freedesktop.DBus.Properties"
ADAPTER = "/org/bluez/" + os.environ.get("BLE_CLIENT_ADAPTER", "hci0")
CHUNK_BYTES = 18

bus = dbus.SystemBus()


def objects() -> dict:
    return dbus.Interface(bus.get_object(BLUEZ, "/"), OBJECT_MANAGER).GetManagedObjects()


def find_device(timeout: int) -> tuple[str, str]:
    adapter = dbus.Interface(bus.get_object(BLUEZ, ADAPTER), "org.bluez.Adapter1")
    adapter.SetDiscoveryFilter({"Transport": "le", "UUIDs": dbus.Array([SERVICE], signature="s")})
    adapter.StartDiscovery()
    try:
        deadline = time.time() + timeout
        while time.time() < deadline:
            for path, interfaces in objects().items():
                device = interfaces.get("org.bluez.Device1")
                if (
                    device
                    and path.startswith(ADAPTER)
                    and SERVICE in [str(uuid) for uuid in device.get("UUIDs", [])]
                ):
                    return path, str(device.get("Name", device.get("Alias", "?")))
            time.sleep(1)
    finally:
        adapter.StopDiscovery()
    raise SystemExit("no setup service found")


def connect(path: str) -> tuple[dbus.Interface, dict]:
    device = dbus.Interface(bus.get_object(BLUEZ, path), "org.bluez.Device1")
    for attempt in range(4):
        try:
            device.Connect(timeout=25)
            break
        except dbus.DBusException as error:
            print(f"connect attempt {attempt + 1}: {error.get_dbus_message()}", flush=True)
            time.sleep(2)
    properties = dbus.Interface(bus.get_object(BLUEZ, path), PROPERTIES)
    for _ in range(120):
        if properties.Get("org.bluez.Device1", "ServicesResolved"):
            break
        time.sleep(0.5)
    else:
        raise SystemExit("services not resolved")
    characteristics = {}
    for object_path, interfaces in objects().items():
        characteristic = interfaces.get("org.bluez.GattCharacteristic1")
        if characteristic and object_path.startswith(path):
            characteristics[str(characteristic["UUID"])] = dbus.Interface(
                bus.get_object(BLUEZ, object_path), "org.bluez.GattCharacteristic1"
            )
    return device, characteristics


def read(characteristic: dbus.Interface) -> str:
    return bytes(characteristic.ReadValue({})).decode()


def send_configuration(characteristics: dict, request_path: str) -> None:
    with open(request_path) as request:
        payload = json.load(request)
    data = json.dumps({"type": "configure", "payload": payload}).encode()
    parts = [data[i : i + CHUNK_BYTES] for i in range(0, len(data), CHUNK_BYTES)]
    for index, part in enumerate(parts):
        characteristics[COMMAND].WriteValue(
            dbus.Array(bytes([index, len(parts)]) + part, signature="y"), {"type": "request"}
        )
    print(f"sent {len(parts)} chunks", flush=True)
    deadline = time.time() + 20
    status = ""
    while time.time() < deadline:
        status = read(characteristics[STATUS])
        if status == "ok" or status.startswith("error"):
            break
        time.sleep(0.4)
    print(f"status after: {status}", flush=True)
    if status != "ok":
        raise SystemExit(1)


def main() -> None:
    if len(sys.argv) < 2 or sys.argv[1] not in ("read", "configure"):
        raise SystemExit(__doc__)
    action = sys.argv[1]
    wait_index = 2 if action == "read" else 3
    wait = int(sys.argv[wait_index]) if len(sys.argv) > wait_index else 150
    path, name = find_device(wait)
    print(f"found: {name}", flush=True)
    device, characteristics = connect(path)
    try:
        networks = json.loads(read(characteristics[SCAN]))
        print(f"wifi list over bluetooth: {len(networks)} networks", flush=True)
        print(f"status before: {read(characteristics[STATUS])}", flush=True)
        if action == "configure":
            send_configuration(characteristics, sys.argv[2])
    finally:
        with contextlib.suppress(dbus.DBusException):
            device.Disconnect()
        time.sleep(1)
        with contextlib.suppress(dbus.DBusException):
            dbus.Interface(bus.get_object(BLUEZ, ADAPTER), "org.bluez.Adapter1").RemoveDevice(path)


if __name__ == "__main__":
    main()
