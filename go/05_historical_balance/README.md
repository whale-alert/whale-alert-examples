# Go Quickstart: Historical Balance at Point in Time

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8.svg?style=flat-square&logo=go)](https://go.dev)
[![API Plan: Enterprise](https://img.shields.io/badge/API%20Plan-Enterprise%20%7C%20Enterprise%20Plus-orange.svg?style=flat-square)](https://whale-alert.io)

Query the exact cryptocurrency or token balance and asset price for any blockchain address at a specific historical point in time using the Whale Alert REST API.

---

## Overview

1. **Resolve Target Block Height**: Queries the height-at-time endpoint (`GET /{blockchain}/height_at_time/{timestamp}`) to resolve the exact block height active at the specified historical timestamp.
2. **Verify Plan Retention Boundaries**: Inspects the blockchain status endpoint (`GET /{blockchain}/status`) to verify `min_plan_height` and ensure the target block falls within your API plan's historical retention window. Note that the Enterprise plan covers the last 90 days (while Enterprise Plus provides full historical access); because balance lookup relies on finding the address's most recent transaction, if an address was not involved in any transaction for that symbol in the last 90 days, its historical balance cannot be retrieved.
3. **Query Address Balance**: Queries the transactions endpoint (`GET /{blockchain}/transactions?address={addr}&symbol={sym}&order=desc&limit=1&end_height={target_block}`) to fetch the single most recent transaction record at or prior to the target block.
4. **Extract Balance & Spot Price**: Extracts the post-transaction address balance (`Account.Balance`), historical unit price in USD (`SubTransaction.UnitPriceUSD`), and entity owner attribution.
5. **Display Portfolio Valuation**: Calculates the estimated portfolio value (`balance * unit_price_usd`) and prints a formatted summary report.

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

*(Alternatively, edit the `whaleAlertApiKey` constant at the top of `main.go`)*

### 2. Run
```bash
cd go/05_historical_balance
go run main.go
```

---

## Command Line Flags

| Flag | Default | Description |
|---|---|---|
| `-blockchain` | `ethereum` | Target blockchain network (e.g. `ethereum`, `bitcoin`, `tron`, `solana`) |
| `-address` | `0xbdb3ba9ffe392549e1f8658dd2630c141fdf47b6` | Target wallet or smart contract address |
| `-symbol` | `ETH` | Target token or coin symbol (e.g. `ETH`, `USDT`, `USDC`, `BTC`) |
| `-time` | `""` (1h ago) | Historical point in time (Unix timestamp, RFC3339 string, or relative duration like `1h`, `24h`, `7d`) |
| `-start` | `0` | Starting block height boundary (defaults to `0` / plan minimum height) |
| `-api-key` | `""` | Optional API key override (defaults to `WHALE_ALERT_API_KEY` env var) |

---

## Usage Examples

### 1. Default Run (1 Hour Ago on Ethereum)
Queries the balance of a prominent active address for `ETH` 1 hour ago:
```bash
go run main.go
```

### 2. Query Balance at a Specific Unix Timestamp
Query the USDT balance of an address at a specific Unix timestamp (e.g. `1787653559`):
```bash
go run main.go -blockchain ethereum -address 0xbdb3ba9ffe392549e1f8658dd2630c141fdf47b6 -symbol USDT -time 1787653559
```

### 3. Query Balance by RFC3339 Date / Time String
Query the balance at a human-readable date and time (UTC):
```bash
go run main.go -blockchain ethereum -address 0xbdb3ba9ffe392549e1f8658dd2630c141fdf47b6 -symbol USDT -time "2026-08-25T10:00:00Z"
```

### 4. Query Balance by Relative Duration
Query the balance exactly 24 hours prior to the current time:
```bash
go run main.go -blockchain ethereum -address 0xbdb3ba9ffe392549e1f8658dd2630c141fdf47b6 -symbol USDT -time 24h
```

---

## Sample Console Output

Captured from an explicit USDT lookup (`-symbol USDT`). The default `-symbol` is `ETH`.

```text
2026/08/26 12:00:00 ===================================================================================
2026/08/26 12:00:00             WHALE ALERT HISTORICAL BALANCE LOOKUP (POINT IN TIME)
2026/08/26 12:00:00 ===================================================================================
2026/08/26 12:00:00 Blockchain      : ethereum
2026/08/26 12:00:00 Target Address  : 0xbdb3ba9ffe392549e1f8658dd2630c141fdf47b6
2026/08/26 12:00:00 Target Symbol   : USDT
2026/08/26 12:00:00 Point in Time   : 2026-08-26T11:00:00Z (Unix: 1787742000)
2026/08/26 12:00:00 -----------------------------------------------------------------------------------
2026/08/26 12:00:00 [1/3] Resolving block height at 2026-08-26 11:00:00 UTC via GET /ethereum/height_at_time/1787742000...
2026/08/26 12:00:01       -> Resolved Block Height #25,831,603 (mined 2026-08-26 11:00:00 UTC, delta: 0s)
2026/08/26 12:00:01 [2/3] Checking blockchain plan boundaries via GET /ethereum/status...
2026/08/26 12:00:01       -> Current Height: #25,831,777 | Minimum Plan Height: #25,164,685
2026/08/26 12:00:01 [3/3] Querying address balance (order=desc, limit=1) up to block #25,831,603...

===================================================================================
                   BALANCE SNAPSHOT AT POINT IN TIME
===================================================================================
Blockchain            : ETHEREUM
Address               : 0xbdb3ba9ffe392549e1f8658dd2630c141fdf47b6
Owner Attribution     : Bitfinex (Type: exchange)
Asset Symbol          : USDT
Requested Time        : 2026-08-26 11:00:00 UTC
Resolved Block Height : #25,831,603 (mined 2026-08-26 11:00:00 UTC)
-----------------------------------------------------------------------------------
BALANCE AT THAT TIME  : 5,848,941.77892 USDT
ASSET PRICE (AT BLOCK): $0.9997 USD
PORTFOLIO VALUE (USD) : $5,847,436.56 USD
===================================================================================
```
