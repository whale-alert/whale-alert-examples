# Python Quickstart: WebSocket Live Stream

[![Python Version](https://img.shields.io/badge/Python-3.10+-3776AB.svg?style=flat-square&logo=python)](https://www.python.org)
[![API Plan: All Plans](https://img.shields.io/badge/API%20Plan-All%20Plans-brightgreen.svg?style=flat-square)](https://whale-alert.io)

Connect to the Whale Alert Live Stream WebSocket API in Python to stream live alerts and socials.

---

## Overview
1. Connects to `wss://leviathan.whale-alert.io/ws?api_key=...` using `asyncio` and the [`websockets`](https://websockets.readthedocs.io) library.
2. Sends the subscription filter payload (`SUBSCRIPTION`) to customize chains, symbols, transaction types, or USD threshold.
3. Dispatches incoming JSON events on their `type` field (`alert`, `socials`, `subscribed_alerts`, `subscribed_socials`).
4. Logs real-time formatted transaction alerts and social posts to the console.
5. Manages reconnections with backoff and handles `SIGINT` / `SIGTERM` signals for graceful shutdown.

Keepalive is handled for you: `websockets` sends its own pings on `ping_interval` and answers server pings automatically, so there is no manual ping loop in this example.

---

## Quick Run

### 1. Set API Key
**Linux / macOS**:
```bash
export WHALE_ALERT_API_KEY="your_api_key_here"
```

**Windows (PowerShell)**:
```powershell
$env:WHALE_ALERT_API_KEY="your_api_key_here"
```

*(Alternatively, pass `--api-key "your_api_key_here"` or edit the `WHALE_ALERT_API_KEY` constant at the top of `main.py`)*

### 2. Install & Run
```bash
cd python/01_websocket_live_stream
pip install -r requirements.txt
python main.py
```

---

## Command Line Flags

| Flag | Default | Description |
|---|---|---|
| `--api-key` | `""` | Optional API key override (defaults to `WHALE_ALERT_API_KEY` env var) |

All other settings are constants at the top of `main.py`.

---

## Customizing the Filter

Edit the `SUBSCRIPTION` dictionary at the top of `main.py`. Leave a list empty to receive everything for that field:

```python
SUBSCRIPTION = {
    "type": "subscribe_alerts",          # or "subscribe_socials"
    "blockchains": ["ethereum", "tron"],  # lowercase; empty = all chains
    "symbols": ["btc", "eth", "usdt"],    # lowercase; empty = all symbols
    "tx_types": ["transfer", "mint"],     # empty = all types
    "min_value_usd": 100_000,
}
```

Supported `tx_types` are `transfer`, `mint`, `burn`, `freeze`, `unfreeze`, `lock`, and `unlock`.

---

## Sample Console Output

```text
2026/08/24 10:15:00 Whale Alert streaming client started. Press Ctrl+C to exit.
2026/08/24 10:15:01 Connected to Whale Alert WebSocket server.
================================================================================
SUBSCRIPTION CONFIRMED
  Blockchains: all
  Symbols:     all
  Tx Types:    all
  Min USD:     $100,000
================================================================================

--------------------------------------------------------------------------------
[ETHEREUM] 2,500.00 ETH ($8,750,000 USD) transferred from Unknown to Binance
   Transaction Type: transfer
   From:             Unknown
   To:               Binance
   Amount:           2,500.00 ETH ($8,750,000.00 USD)
   Hash:             0x4a9b...7c1e
   Block Height:     #20589123
   Timestamp:        2026-08-24 08:15:10 UTC (UNIX: 1724487310)
--------------------------------------------------------------------------------
```
