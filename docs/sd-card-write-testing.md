# SD Card Write Testing

Use two complementary tests to evaluate a TeslaUSB card:

1. **Tesla workload side:** reproduce the four-camera recording pattern and confirm that the card sustains the required data rate.
2. **Card capability side:** measure sequential write throughput independently of the Tesla workload.

Run both when investigating a slow-card warning. A workload test shows whether recording has enough headroom; a capability test helps distinguish a weak card from host, filesystem, or benchmark overhead.

## Safety and test conditions

- Test only a mounted, writable filesystem with enough free space.
- Do not run a raw benchmark while the car is recording or while archiving is active.
- Use only a uniquely named temporary test file or directory. Remove it afterward.
- Treat source clips as read-only.
- Compare runs only when the host, filesystem, data source, block size, sync flags, and test size match.
- Repeat important measurements. A single run can be affected by card state, temperature, USB readers, and other I/O.

## Side 1: Tesla-like four-camera workload

`tools/sustained-write-test.py` writes four concurrent camera streams. By default it runs for five minutes and starts a new four-file cycle every 60 seconds.

Each one-minute clip is written progressively in approximately one-second slices. The helper preserves each source file's size by default; `--size-mb` provides an explicit override for synthetic sources. Source clips are never modified.

A representative Model 3 workload is approximately:

- 36 MiB per camera per minute;
- four cameras, giving 144 MiB/min;
- five cycles, giving 720 MiB total;
- 2.52 MB/s aggregate application throughput in decimal units.

Example using one four-camera source set:

```bash
python3 tools/sustained-write-test.py \
  --target /run/media/$USER/CAM \
  --clips \
    /tmp/tesla-clips/2026-11-07_12-00-00-front.mp4 \
    /tmp/tesla-clips/2026-11-07_12-00-00-back.mp4 \
    /tmp/tesla-clips/2026-11-07_12-00-00-left_repeater.mp4 \
    /tmp/tesla-clips/2026-11-07_12-00-00-right_repeater.mp4 \
  --duration 300 \
  --cycle-seconds 60 \
  --size-mb 36
```

The generated layout is:

```text
.teslausb-write-test-<timestamp>-<pid>/
  cycle-0000/
    2026-11-07_12-00-00-front.mp4
    2026-11-07_12-00-00-back.mp4
    2026-11-07_12-00-00-left_repeater.mp4
    2026-11-07_12-00-00-right_repeater.mp4
  cycle-0001/
    ...
```

The helper checks free space, refuses an unmounted target, flushes each clip when it closes, and removes its own test directory unless `--keep` is supplied. Clips larger than 64 MiB also have intermediate 64 MiB sync points.

The final JSON reports bytes, elapsed time, per-camera rate, aggregate rate, sync calls, and cleanup status. Its rate is **application throughput over the whole test**, including pacing and final flushes—not the card's maximum speed.

## Side 2: card capability tests

### Pi/web durable-block benchmark

`run/benchmark.sh` is installed as `/root/bin/benchmark.sh` and exposed by the web UI. It writes 160 MiB using 4 MiB blocks:

```bash
sudo /root/bin/benchmark.sh
sudo /root/bin/benchmark.sh --check
```

The benchmark uses `/dev/zero` and `oflag=dsync conv=fsync`. Each 4 MiB block waits for durable completion, making this a conservative storage test rather than a page-cache benchmark. It has a 60-second timeout, removes its test file, and retains the last three JSON results in `/mutable/sdcard_benchmark.json`.

Do not use `/dev/urandom` for this test on the Pi Zero 2 W: random-data generation can consume enough CPU to distort the storage result.

### 1 GiB direct-I/O sequential test

On a Linux host with the card mounted, this measures a longer sequential write while bypassing the Linux page cache:

```bash
target=/run/media/$USER/CAM
mountpoint -q "$target"
test -w "$target"
test_file="$target/.teslausb-1gib-speed-test-$$.tmp"
trap 'rm -f "$test_file"' EXIT
TIMEFORMAT='elapsed_seconds=%3R'
time dd if=/dev/zero of="$test_file" \
  bs=4M count=256 oflag=direct conv=fdatasync status=none
```

`oflag=direct` bypasses the page cache; `conv=fdatasync` includes a final flush. Unlike the Pi/web benchmark, it does **not** wait for durable completion after every 4 MiB block. The two results are therefore complementary, not interchangeable.

## Buffered writes versus physical card writes

File growth measures how quickly the application supplies data. It does not necessarily show when data reaches the card. Linux can cache writes and flush them later, including at clip close.

For physical rates, sample the card's block-device counters in `/proc/diskstats` over a known interval. A short sample can show zero physical writes while file data is still cached, followed by a high-rate flush. A one-second flush peak must not be reported as sustained card throughput.

## Measurements from 2026-11-07

These are recorded examples, not permanent performance claims.

| Host and method | Input and size | Elapsed | Result |
|---|---|---:|---:|
| Linux PC, four-camera paced workload | Zeros, 4 × 36 MiB per cycle, five cycles | 302.248 s | 2.50 MB/s aggregate |
| Linux PC, direct I/O with final flush | Zeros, 1 GiB, 4 MiB blocks | 53.257 s | 20.16 MB/s (19.22 MiB/s) |
| Linux PC, Pi script | Random, 160 MiB, 4 MiB `dsync` | 10.022 s | 16.0 MiB/s |
| Linux PC, Pi script | Zeros, 160 MiB, 4 MiB `dsync` | 10.623 s | 15.1 MiB/s |
| Pi Zero 2 W, original web script | Random, 160 MiB, 4 MiB `dsync` | 14.674 s | 10.9 MiB/s |
| Pi Zero 2 W, updated web script | Zeros, 160 MiB, 4 MiB `dsync` | 9.132 s | 17.5 MiB/s |

The Pi's zero-data result was about 60% faster than its random-data result. On the Linux PC, random versus zero input did not materially change the result. This confirms that random generation was a significant Pi benchmark bottleneck, but host comparisons still require identical conditions.

The Tesla-like workload needed approximately 2.52 MB/s, about 12.5% of the 20.16 MB/s measured by the longer Linux PC direct-I/O test. That is workload headroom for that card/host combination, not a guarantee for another card or the in-car Pi environment.

## Units

The helper's `MB/s` values are decimal (`1 MB = 1,000,000 bytes`). The shell benchmark calculates with `1 MiB = 1,048,576 bytes` even though its existing output label says `MB/s`. Convert units before comparing results.

## Related Documentation

- [`power-benchmarking.md`](power-benchmarking.md) — Automated BLE power & throughput benchmarking across CPU governors, frequency steps, core counts, and `devfreq` states using `tools/power-benchmark.py`.
