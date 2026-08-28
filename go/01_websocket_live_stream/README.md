# Go Quickstart: WebSocket Live Stream

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8.svg?style=flat-square&logo=go)](https://go.dev)
[![API Plan: All Plans](https://img.shields.io/badge/API%20Plan-All%20Plans-brightgreen.svg?style=flat-square)](https://whale-alert.io)

Connect to the Whale Alert Live Stream WebSocket API in Go to stream live alerts and socials.

---

## Overview
1. Connects to `wss://leviathan.whale-alert.io/ws?api_key=...`.
2. Sends the subscription filter payload (`AlertSubscriptionJSON`) to customize chains, symbols, transaction types, or USD threshold.
3. Unmarshals incoming JSON events into strongly-typed Go structs (`AlertJSON`, `SocialsJSON`).
4. Logs real-time formatted transaction alerts and social posts to the console.
5. Manages reconnections with backoff and handles `SIGINT` / `SIGTERM` signals for graceful shutdown.

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

*(Alternatively, pass `-api-key="your_api_key_here"` or edit the `whaleAlertApiKey` constant at the top of `main.go`)*

### 2. Run
```bash
cd go/01_websocket_live_stream
go run main.go
```

---

## Command Line Flags

| Flag | Default | Description |
|---|---|---|
| `-api-key` | `""` | Optional API key override (defaults to `WHALE_ALERT_API_KEY` env var) |

All other settings are constants at the top of `main.go`.

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
