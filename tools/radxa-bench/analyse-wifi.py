"""Summarise antenna-test.sh CSVs: one row per run (mode or antenna)."""

import csv
import statistics as st
import sys
from collections import Counter
from itertools import pairwise

print(
    f"{'run':18} {'bssid':>17} {'MHz':>5} {'idle dBm':>8} {'load dBm':>8} {'tx Mbit':>7} {'top MCS':>9} {'MB/s':>6} {'p10 MB/s':>8} {'errs':>5} {'max C':>5}"
)
for path in sys.argv[1:]:
    with open(path) as f:
        rows = list(csv.DictReader(f))
    if not rows:
        continue
    idle = [r for r in rows if r["phase"] == "idle"]
    up = [r for r in rows if r["phase"] == "upload"]

    def sig(rs):
        v = [int(r["signal_dbm"]) for r in rs if r["signal_dbm"].lstrip("-").isdigit()]
        return st.median(v) if v else float("nan")

    rates = []
    for a, b in pairwise(up):
        dt = int(b["epoch"]) - int(a["epoch"])
        if dt > 0:
            rates.append((int(b["wlan_tx_bytes"]) - int(a["wlan_tx_bytes"])) / dt / 1e6)
    total = (
        (int(up[-1]["wlan_tx_bytes"]) - int(up[0]["wlan_tx_bytes"]))
        / max(int(up[-1]["epoch"]) - int(up[0]["epoch"]), 1)
        / 1e6
        if len(up) > 1
        else 0
    )
    p10 = sorted(rates)[len(rates) // 10] if rates else 0
    txm = [float(r["tx_mbit"]) for r in up if r["tx_mbit"]]
    mcs = Counter(r["tx_mcs"] for r in up if r["tx_mcs"]).most_common(1)
    errs = int(up[-1]["wlan_tx_errors"]) - int(up[0]["wlan_tx_errors"]) if up else 0
    bssids = Counter(r["bssid"] for r in rows).most_common(1)[0][0]
    freq = Counter(r["freq_mhz"] for r in rows).most_common(1)[0][0]
    tmax = max(int(r["soc_mC"]) for r in rows) / 1000
    label = rows[0]["label"]
    print(
        f"{label:18} {bssids:>17} {freq:>5} {sig(idle):8.0f} {sig(up):8.0f} {st.median(txm) if txm else 0:7.0f} "
        f"{(mcs[0][0] if mcs else '-'):>9} {total:6.1f} {p10:8.1f} {errs:5d} {tmax:5.1f}"
    )
