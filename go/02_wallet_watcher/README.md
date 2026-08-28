# Go Quickstart: Wallet Watcher

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8.svg?style=flat-square&logo=go)](https://go.dev)
[![API Plan: Enterprise](https://img.shields.io/badge/API%20Plan-Enterprise%20%7C%20Enterprise%20Plus-orange.svg?style=flat-square)](https://whale-alert.io)

Real-time address monitoring using the Whale Alert REST API.

---

## Overview
1. Queries the blockchain status endpoint (`GET /{blockchain}/status`) to get the latest block height (`end_height`).
2. Queries the transactions endpoint filtered by address (`GET /{blockchain}/transactions?start_height={end_height}&address={watched_address}`).
3. Keeps polling periodically using the `next` URL cursor returned in each response.
4. Analyzes transactions involving the watched address.
5. Prints notifications when the watched address is detected as sender, receiver, or in a self-transfer.

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
cd go/02_wallet_watcher
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
2026/08/24 13:20:00 Monitoring ethereum address 0x28c6c06298d514db089934071355e5743bf21d60 (Binance 14) from block #20589120...

[WATCHLIST MATCH] Binance 14 Wallet Activity
   Direction:   INCOMING
   Asset:       500.00 ETH ($1,750,000.00 USD)
   Price:       $3,500.00 USD / ETH
   From:        0x742d...44e (Unknown)
   To:          0x28c6c06298d514db089934071355e5743bf21d60 (Binance 14)
   Tx Hash:     0x8e12...33fa
   Block:       #20589123
   Timestamp:   2026-08-24 13:20:05 UTC
--------------------------------------------------------------------------------
```
