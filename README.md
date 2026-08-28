# Whale Alert API Code Examples

[![Documentation](https://img.shields.io/badge/docs-whale--alert.io-blue.svg?style=flat-square)](https://docs.whale-alert.io)
[![Supported Chains & Symbols](https://img.shields.io/badge/status-supported%20chains%20%26%20symbols-brightgreen.svg?style=flat-square)](https://leviathan.whale-alert.io/status?format=true)
[![Go Examples](https://img.shields.io/badge/Go-1.22+-00ADD8.svg?style=flat-square&logo=go)](go/)
[![Python Examples](https://img.shields.io/badge/Python-3.10+-3776AB.svg?style=flat-square&logo=python)](python/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)](LICENSE)

Official, standalone code examples and recipes for integrating with Whale Alert APIs.

To check active blockchain networks, assets, and tracked symbols, visit the live status dashboard: [leviathan.whale-alert.io/status?format=true](https://leviathan.whale-alert.io/status?format=true).

---

## Directory Structure

This repository is organized language-first:

```
whale-alert-examples/
├── go/                                   # Go Quickstarts (Recommended for low-latency pipelines)
│   ├── 01_websocket_live_stream/        # Connect, authenticate, handle heartbeats, stream events
│   ├── 02_wallet_watcher/               # Multi-chain address and contract monitoring
│   ├── 03_stablecoin_mints_burns/        # Real-time stablecoin mints, burns, (un)freezes & (un)locks
│   ├── 04_historical_query/             # REST API pagination and historical aggregation
│   ├── 05_historical_balance/           # Historical address balance and price at a specific timestamp
│   └── 06_reorg_detector/               # Real-time chain reorganization, orphan & uncle block detection
│
└── python/                               # Python Quickstarts
    ├── 01_websocket_live_stream/        # Asynchronous WebSocket consumer
    ├── 02_wallet_watcher/               # Multi-chain address monitor
    ├── 03_stablecoin_mints_burns/        # Real-time stablecoin mints, burns, (un)freezes & (un)locks
    ├── 04_historical_query/             # REST historical query and metrics
    ├── 05_historical_balance/           # Historical address balance and price at a specific timestamp
    └── 06_reorg_detector/               # Real-time chain reorganization, orphan & uncle block detection
```

---

## Quickstart Matrix

| Index | Name | Focus | Go | Python | Target Plan |
|---|:---|:---|:---:|:---:|:---|
| `01` | **WebSocket Live Stream** | Stream connection, auth handshake, and ping/pong keepalive. | [`go/01`](go/01_websocket_live_stream/) | [`python/01`](python/01_websocket_live_stream/) | All Plans (Live alerts only) |
| `02` | **Wallet Watcher** | Track specific addresses | [`go/02`](go/02_wallet_watcher/) | [`python/02`](python/02_wallet_watcher/) | Enterprise / Enterprise Plus |
| `03` | **Stablecoin Mints, Burns, Freezes & Locks** | Track real-time stablecoin issuance (mints), burns, (un)freezes, and (un)locks. | [`go/03`](go/03_stablecoin_mints_burns/) | [`python/03`](python/03_stablecoin_mints_burns/) | Enterprise / Enterprise Plus |
| `04` | **Historical Query** | REST API range queries, cursor pagination, and volume totals. | [`go/04`](go/04_historical_query/) | [`python/04`](python/04_historical_query/) | Enterprise / Enterprise Plus |
| `05` | **Historical Balance at Point in Time** | Query address balance and asset price at a specific historical timestamp. | [`go/05`](go/05_historical_balance/) | [`python/05`](python/05_historical_balance/) | Enterprise / Enterprise Plus |
| `06` | **Chain Reorganization & Orphan Detector** | Real-time reorg detection, sliding block cache, and orphan depth calculation. | [`go/06`](go/06_reorg_detector/) | [`python/06`](python/06_reorg_detector/) | Enterprise / Enterprise Plus |

---

## Authentication & Setup

All examples read the API key from the `WHALE_ALERT_API_KEY` environment variable.

### 1. Get an API Key
Sign up at the [Whale Alert Developer Portal](https://developer.whale-alert.io/api-account/login).

### 2. Set the Environment Variable

**Linux / macOS**:
```bash
export WHALE_ALERT_API_KEY="your_api_key_here"
```

**Windows (PowerShell)**:
```powershell
$env:WHALE_ALERT_API_KEY="your_api_key_here"
```

---

## Running Examples

### Go (Go 1.22+)
```bash
cd go/01_websocket_live_stream
go run main.go
```
See the [Go Guide](go/README.md) for details.

### Python (Python 3.10+)
```bash
cd python/01_websocket_live_stream
pip install -r requirements.txt
python main.py
```
See the [Python Guide](python/README.md) for details.

---

## Data Model

Transactions received via the WebSocket or REST API follow a standardized schema. Every transaction carries block metadata and fee information, plus one or more **sub-transactions**: the individual asset movements (transfers, mints, burns, freezes, locks) contained in the transaction. Each sub-transaction includes the USD unit price (`unit_price_usd`) of the moved asset at the block timestamp, and lists the participating addresses as `inputs` (senders) and `outputs` (receivers) with their post-transaction balances.

```json
{
  "height": 44204309,
  "index_in_block": 3,
  "timestamp": 1787735817,
  "hash": "0x489057d96f773fe596f869bb7822dda44aafb86334510a28c8de9bd17662c29f",
  "fee": "0.000436038733996662",
  "fee_symbol": "HYPE",
  "fee_symbol_price": 81.74165,
  "sub_transactions": [
    {
      "symbol": "USDC",
      "unit_price_usd": 0.999999875,
      "transaction_type": "mint",
      "inputs": [],
      "outputs": [
        {
          "amount": "499999.8",
          "address": "0xb21d281dedb17ae5b501f6aa8256fe38c4e45757",
          "balance": "14082.194469",
          "address_type": "coinbase"
        }
      ]
    },
    {
      "symbol": "USDC",
      "unit_price_usd": 0.999999875,
      "transaction_type": "transfer",
      "inputs": [
        {
          "amount": "499999.8",
          "address": "0xb21d281dedb17ae5b501f6aa8256fe38c4e45757",
          "balance": "14082.194469",
          "address_type": "coinbase"
        }
      ],
      "outputs": [
        {
          "amount": "499999.8",
          "address": "0x6b9e773128f453f5c2c60935ee2de2cbc5390a24",
          "balance": "613079295.644111",
          "owner": "usdc treasury",
          "owner_type": "bank",
          "address_type": "coinbase"
        }
      ]
    }
  ]
}
```

### Transaction fields

| Field | Type | Description |
|:---|:---|:---|
| `height` | number | Block height containing the transaction. |
| `index_in_block` | number | Position of the transaction within the block. |
| `timestamp` | number | UNIX timestamp of the block. |
| `hash` | string | Transaction hash. |
| `fee` | string | Transaction fee, in the fee currency. |
| `fee_symbol` | string | Symbol the fee is denominated in (e.g. `HYPE`, `ETH`). |
| `fee_symbol_price` | number | USD price per unit of the fee symbol at block timestamp. |
| `sub_transactions` | array | Individual asset movements within the transaction. |

### Sub-transaction fields

| Field | Type | Description |
|:---|:---|:---|
| `symbol` | string | Symbol of the moved asset (e.g. `USDC`, `HYPE`). |
| `unit_price_usd` | number | **USD price per single unit** of `symbol` at block timestamp. Multiply by `amount` for the USD value of a movement, or by `balance` for the USD value of an address holding. |
| `transaction_type` | string | `transfer`, `mint`, `burn`, `freeze`, `unfreeze`, `lock`, or `unlock`. |
| `inputs` | array | Senders. Empty for `mint`. |
| `outputs` | array | Receivers. Empty for `burn`. |

### Address fields (`inputs` / `outputs`)

| Field | Type | Description |
|:---|:---|:---|
| `amount` | string | Amount by which this address balance changed. Decimal string, to preserve precision. |
| `address` | string | Blockchain address. |
| `balance` | string | Address balance for `symbol` after the transaction. |
| `locked` | string | Locked (non-spendable) portion of the balance, where the chain supports it. |
| `is_frozen` | bool | Whether the address is frozen for this asset. |
| `owner` | string | Identified entity, when known (e.g. `binance`, `usdc treasury`). |
| `owner_type` | string | Entity classification (e.g. `exchange`, `bank`, `defi`). |
| `address_type` | string | Address classification (e.g. `hot_wallet`, `cold_wallet`). |

Amounts and balances are decimal **strings** rather than floats so that no precision is lost on large or high-decimal values; convert them explicitly (in Go, `strconv.ParseFloat` or a decimal library) before doing arithmetic.

- **Normalized**: Uniform field names across all 13+ blockchain networks.
- **Post-Transaction Address Balances**: Each transaction also contains the exact resulting balance of each participating address at the end of the transaction.
- **Live Per-Block Attribution**: Analytics run dynamically on each newly confirmed block as it arrives through the API, identifying and tagging new addresses and wallet clusters in real time.
- **Multi-Source Pricing**: `unit_price_usd` is computed at block timestamp using aggregated spot rates across multiple exchange sources.
- **Verified Whitelist**: Tokens are authenticated against official smart contract addresses, eliminating fake or impersonated tokens.
- **Complete Extraction**: Captures internal contract calls, multi-calls, and batch transfers out of the box without custom node tracing or EVM log parsing.

---

## Resources

- Documentation: [docs.whale-alert.io](https://docs.whale-alert.io)
- Live Supported Chains & Symbols: [leviathan.whale-alert.io/status?format=true](https://leviathan.whale-alert.io/status?format=true)
- Developer Portal: [developer.whale-alert.io/api-account/login](https://developer.whale-alert.io/api-account/login)
- Contact: [developers@whale-alert.io](mailto:developers@whale-alert.io) | [enterprise@whale-alert.io](mailto:enterprise@whale-alert.io)
