"""Summarise thermal.csv from thermal-test.sh per phase."""

import csv
import sys
from collections import OrderedDict

with open(sys.argv[1]) as f:
    rows = list(csv.DictReader(f))
phases = OrderedDict()
for r in rows:
    phases.setdefault(r["phase"], []).append(r)

print(
    f"{'phase':9} {'min':>5} {'avg':>5} {'max':>5} {'end':>5} {'gpu max':>7} {'MHz avg':>7} {'MHz min':>7} {'wifi MB/s':>9} {'>=68C s':>7} {'>=75C s':>7} {'n':>4}"
)
for name, rs in phases.items():
    soc = [int(r["soc_mC"]) / 1000 for r in rs]
    gpu = [int(r["gpu_mC"]) / 1000 for r in rs]
    mhz = [int(r["cpu_khz"]) / 1000 for r in rs]
    t0, t1 = int(rs[0]["epoch"]), int(rs[-1]["epoch"])
    tx = (int(rs[-1]["wlan_tx_bytes"]) - int(rs[0]["wlan_tx_bytes"])) / max(t1 - t0, 1) / 1e6
    tail = soc[-30:]  # last ~60 s
    hot68 = sum(1 for s in soc if s >= 68) * 2
    hot75 = sum(1 for s in soc if s >= 75) * 2
    print(
        f"{name:9} {min(soc):5.1f} {sum(soc) / len(soc):5.1f} {max(soc):5.1f} {sum(tail) / len(tail):5.1f} "
        f"{max(gpu):7.1f} {sum(mhz) / len(mhz):7.0f} {min(mhz):7.0f} {tx:9.1f} {hot68:7d} {hot75:7d} {len(rs):4d}"
    )

throttle = {r["throttle_state"] for r in rows}
print("cooling_device0 states seen:", sorted(throttle))
print("governors seen:", sorted({r["governor"] for r in rows}))
