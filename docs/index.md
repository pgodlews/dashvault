# Documentation Index

AI agent and developer reference for the `pgodlews/dashvault` fork.

## Start Here

| Doc | Read when you need to... |
|-----|--------------------------|
| [`architecture.md`](architecture.md) | Understand the system as a whole — components, Mermaid data flows, key concepts |
| [`../AGENTS.md`](../AGENTS.md) | Repository rules, verification commands, Git remotes, and live-hardware safety |

## Setting Up a Pi

| Guide | Use it when... |
|-------|----------------|
| [`setup-wifi-hotspot.md`](setup-wifi-hotspot.md) | You want the illustrated setup over the Pi's `Dashvault-XXXX` hotspot, from any phone or laptop |
| [`setup-bluetooth.md`](setup-bluetooth.md) | You want the illustrated setup over Bluetooth, from Chrome or Edge |

## Deep Dives

| Doc | Read when you're working on... |
|-----|-------------------------------|
| [`go-backend.md`](go-backend.md) | The Go HTTP service — routes, middleware, status collection, CSRF, rate limiting, audit log |
| [`new-ui.md`](new-ui.md) | The React PWA — routes, components, lib helpers, hooks, API patterns |
| [`provisioning.md`](provisioning.md) | Headless BLE, captive-portal, and web setup — hardware-adaptive Mermaid flow |
| [`hardware-testing.md`](hardware-testing.md) | Wi-Fi security lab and first-run setup checks on real boards |
| [`snapshots.md`](snapshots.md) | How RecentClips are kept: snapshot loop, reflink copies, autofs links, free space, clip lifecycle, experimental camera pruning (Mermaid) |
| [`shell-layer.md`](shell-layer.md) | archiveloop, send-push-message, Telegram bot, config backup, setup scripts |
| [`dev-workflow.md`](dev-workflow.md) | CI, testing, branch conventions, deployment |
| [`sd-card-write-testing.md`](sd-card-write-testing.md) | Tesla-like write workloads, raw card throughput, buffering, and benchmark results |
| [`power-benchmarking.md`](power-benchmarking.md) | USB 2.0/3.0 power budget analysis, BLE power meter benchmark (`tools/power-benchmark.py`), and Radxa / RPi Zero 2 W power profiles |
| [`radxa-thermal-and-antennas.md`](radxa-thermal-and-antennas.md) | Radxa Zero 3W temperatures under load with a heatsink, enabling the external antenna, and built-in vs external antenna Wi-Fi results (`tools/radxa-bench/`) |

## Extension Guides

| Doc | Use when you want to... |
|-----|------------------------|
| [`extending/add-notifier.md`](extending/add-notifier.md) | Add a new notification channel (Apprise, custom webhook, etc.) |
| [`extending/add-archive-backend.md`](extending/add-archive-backend.md) | Add a new archive backend (S3, custom storage, etc.) |

## Quick Answers

**Where does `/api/status` data come from?**  
→ `collect.go` → `Collector.Collect()` → reads `/proc`, `/sys`, `statfs`, `iwgetid`, `/run/teslausb/archive_state`

**How does the UI authenticate API calls?**  
→ HTTP Basic Auth on every request, checked by the Go backend through nginx `auth_request` (with a lockout after repeated wrong passwords); CSRF cookie+header for mutating requests. See [`go-backend.md`](go-backend.md#csrf-details).

**How do I trigger an archive run from code?**  
→ Touch `/tmp/archive_is_unreachable`. Used by UI trigger_sync button and Telegram `/sync`.

**What's the test command?**  
→ `just ci` (full). `just test` (Python only). `just test-backend` (Go only).

**What PR number am I up to?**  
→ Check [pull requests on GitHub](https://github.com/pgodlews/dashvault/pulls). The trunk is `main`.

**How do I benchmark SD-card writes?**
→ Use the [two-sided write-testing guide](sd-card-write-testing.md): Tesla-like workload first, raw card capability second.
