# Python Quickstart: Chain Reorganization & Orphan Block Detector

[![Python Version](https://img.shields.io/badge/Python-3.10+-3776AB.svg?style=flat-square&logo=python)](https://www.python.org)
[![API Plan: Enterprise](https://img.shields.io/badge/API%20Plan-Enterprise%20%7C%20Enterprise%20Plus-orange.svg?style=flat-square)](https://whale-alert.io)

Real-time blockchain reorganization (reorg), orphan, and uncle block detector using the Whale Alert REST API.

---

## Overview

A **chain reorganization (reorg)** occurs when a node detects a longer or heavier valid chain branch, replacing previously accepted blocks at recent heights. Blocks on the abandoned branch become **orphaned** (or **uncles** / **stale blocks**), and their state changes are reverted.

This quickstart continuously monitors the canonical blockchain tip, verifies previous block integrity on every new block, detects reorgs in real time, and walks backward to find the exact common ancestor and reorg depth.

```
                    [Fork Point] (Common Ancestor)
                          │
                          ├─► [Orphan Block #H-1] ──► [Orphan Block #H] (Abandoned Branch)
                          │
                          └─► [Canonical #H-1]    ──► [Canonical #H]   ──► [New Block #H+1] (Canonical Chain)
```

---

## How It Works

1. **Resolve Initial Chain Tip**: Queries `GET /{blockchain}/status` to obtain the latest confirmed block height (`end_height`).
2. **Pre-seed Sliding Window Cache**: Fetches initial block hashes (`GET /{blockchain}/block/{height}`) to populate a rolling in-memory cache of the last *N* blocks (default: 20 blocks).
3. **Continuous Forward Polling**: Monitors for new blocks arriving at `current_tip + 1`.
4. **Step-Ahead Verification (H-1)**: When a new block *H* arrives, the detector queries block *H-1* from the network and compares its hash against the locally cached hash `cache[H-1]`:
   - **No Reorg (`hash == cached.hash`)**: The chain advanced linearly. The new block is added to the sliding cache, and tracking advances.
   - **Reorg Detected (`hash != cached.hash`)**: A reorganization occurred. The detector logs a warning alert and initiates a backward search.
5. **Backward Ancestor Traversal**: The detector steps back (*H-2*, *H-3*, …) querying network blocks until `network[k].hash == cache[k].hash`. The matching height *k* is the **Common Ancestor**.
6. **Depth & Impact Reporting**:
   - Computes **Reorganization Depth**: the number of blocks discarded from the old branch.
   - Lists all **Orphaned Block Hashes** (old branch) vs. **New Canonical Hashes** (new branch).
   - Reconciles the local cache with the new canonical hashes and resumes live tracking.

Hash comparisons are case-insensitive, since block hashes are returned in mixed case on some chains.

---

## Prerequisites & Setup

1. **Whale Alert API Key**: Obtain an API key from the [Whale Alert Developer Portal](https://developer.whale-alert.io/api-account/login).
2. **Environment Variable**: Set your API key in the `WHALE_ALERT_API_KEY` environment variable:

**Linux / macOS**:
```bash
export WHALE_ALERT_API_KEY="your_api_key_here"
```

**Windows (PowerShell)**:
```powershell
$env:WHALE_ALERT_API_KEY="your_api_key_here"
```

*(Alternatively, pass `--api-key "your_api_key_here"` or edit the `WHALE_ALERT_API_KEY` constant in `main.py`)*

3. **Install dependencies**:
```bash
cd python/06_reorg_detector
pip install -r requirements.txt
```

---

## Running the Example

### 1. Default Live Monitoring (Ethereum)
Monitors Ethereum mainnet in real time starting from the latest confirmed block:

```bash
python main.py
```

### 2. Monitor Bitcoin (UTXO Chain)
Monitors Bitcoin blocks with a 10-second polling interval:

```bash
python main.py --blockchain bitcoin --poll 10s
```

### 3. Custom Starting Height & History Window
Start tracking from a specific historical block height with a 50-block sliding window:

```bash
python main.py --blockchain ethereum --start 25840400 --history 50 --poll 2s
```

---

## Command Line Flags

| Flag | Default | Description |
|---|---|---|
| `--blockchain` | `ethereum` | Target blockchain network (e.g. `ethereum`, `bitcoin`, `tron`, `dogecoin`) |
| `--history` | `20` | Number of recent block hashes to retain in the sliding window cache (minimum `5`) |
| `--poll` | `3s` | Polling interval when awaiting new blocks (`3s`, `500ms`, `1m`, or a bare number of seconds; clamped to a `500ms` minimum) |
| `--start` | `0` | Starting block height (`0` for latest network block) |
| `--api-key` | `""` | Optional API key override (defaults to `WHALE_ALERT_API_KEY` env var) |

---

## A Note on Request Volume

Every new block costs two `GET /{blockchain}/block/{height}` calls — one for the arriving block, one to re-verify the previous one — plus one call per block walked during an ancestor search, and `--history` calls at startup to seed the window. On a fast chain with a short `--poll`, that adds up against your plan's rate limit. Raise `--poll` if you see `HTTP 429`.

---

## Sample Console Output

### Live Block Tracking & Progression

```text
===================================================================================
           WHALE ALERT CHAIN REORGANIZATION & ORPHAN DETECTOR (ETHEREUM)
===================================================================================
Blockchain Network  : ETHEREUM
Starting Height     : #25,840,430
Sliding Cache Window: 20 blocks
Polling Interval    : 3s
===================================================================================
2026/08/26 16:00:00 Pre-seeding cache with 20 initial blocks (#25,840,411 to #25,840,430)...
2026/08/26 16:00:01 Cache initialized with 20 blocks. Latest tip: #25,840,430 (0x3a9b8c21f0...b41d7c1e)
-----------------------------------------------------------------------------------
2026/08/26 16:00:01 Starting real-time block watcher and reorg detection loop...
-----------------------------------------------------------------------------------
[16:00:12] Block #25,840,431 | Hash: 0x8e12a4bd03...9f2c33fa | 185 txs | Mined: 12s ago | Reorgs: 0
[16:00:24] Block #25,840,432 | Hash: 0x4f7ce90a15...c73899b1 | 210 txs | Mined: 12s ago | Reorgs: 0
[16:00:36] Block #25,840,433 | Hash: 0x1d4a99fc00...21b055e2 | 194 txs | Mined: 12s ago | Reorgs: 0
```

### Detected Reorganization Alert

```text
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
⚠️  ALERT: BLOCKCHAIN REORGANIZATION (REORG / ORPHAN) DETECTED!
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
Detected At         : 2026-08-26 16:00:48.125 UTC
Blockchain Network  : ETHEREUM
Reorganization Depth: 1 BLOCK(S)
Common Ancestor     : Block #25,840,432 (Hash: 0x4f7ce90a15...c73899b1)
-----------------------------------------------------------------------------------
DISCARDED / ORPHANED BLOCKS (Old Branch):
  [1] Height #25,840,433 | Orphan Hash   : 0x8a92f03b41d8e...e9a1 (194 txs)
-----------------------------------------------------------------------------------
NEW CANONICAL REPLACEMENT BLOCKS (New Branch):
  [1] Height #25,840,433 | Canonical Hash: 0x1d4a99fc0021b...55e2 (196 txs)
===================================================================================
```

### Shutdown Summary Report

```text
===================================================================================
                   REORG DETECTOR SUMMARY REPORT (ETHEREUM)
===================================================================================
Total Elapsed Time  : 1m24s
Blocks Monitored    : 6
Reorg Events Count  : 1
Total Orphan Blocks : 1
Max Reorg Depth     : 1 block(s)
Active Cache Window : #25,840,426 to #25,840,435 (10 blocks)
===================================================================================
```
