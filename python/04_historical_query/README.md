# Python Quickstart: Historical Query (Block Range)

[![Python Version](https://img.shields.io/badge/Python-3.10+-3776AB.svg?style=flat-square&logo=python)](https://www.python.org)
[![API Plan: Enterprise](https://img.shields.io/badge/API%20Plan-Enterprise%20%7C%20Enterprise%20Plus-orange.svg?style=flat-square)](https://whale-alert.io)

Demonstrates how to query historical blockchain transaction data over a specific block height range (`start_height` to `end_height`) using the Whale Alert REST API. Includes cursor-based pagination, rate-limit retry handling with exponential backoff, and multi-metric aggregation (total USD volume, symbol volume breakdown, transaction types, top transfers, and address statistics).

---

## Key Features

- **Block Height Range Query**: Query historical transactions between specific block heights (`--start` to `--end`), or calculate lookback offsets automatically based on current network height (`--blocks`).
- **Cursor Pagination**: Follows response `next` pagination links seamlessly across large datasets.
- **Resilient Request Client**: Includes automatic retry logic with exponential backoff for handling `HTTP 429 Too Many Requests` or transient server errors.
- **USD Value Filtering**: Filters high-value whale transactions based on a customizable minimum USD threshold (`--min-usd`).
- **Comprehensive Aggregation Engine**:
  - Total USD transaction volume
  - Volume breakdown and transfer count per symbol (e.g. BTC, ETH, USDT, USDC, WBTC)
  - Distribution of transaction types (`transfer`, `mint`, `burn`, `freeze`, `lock`)
  - Top 5 highest-value transfers within the block range
  - Unique sender and receiver address counting

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

3. **Install dependencies**:
```bash
cd python/04_historical_query
pip install -r requirements.txt
```

---

## Running the Example

### 1. Default Run (Last 10 Blocks on Ethereum)
If `--start` and `--end` are not specified, the example fetches the latest confirmed block height from `GET /ethereum/status` and queries the most recent 10 blocks with a limit of 256 transactions per page retrieval:

```bash
python main.py
```

### 2. Custom Block Range & USD Threshold
Query specific block heights on Ethereum with a $250,000 USD minimum threshold:

```bash
python main.py --blockchain ethereum --start 20000000 --end 20000100 --min-usd 250000 --limit 100
```

### 3. Quick Lookback Range (e.g. Last 500 Blocks on Bitcoin)
Query the last 500 blocks on Bitcoin:

```bash
python main.py --blockchain bitcoin --blocks 500 --min-usd 500000
```

---

## Command Line Flags

| Flag | Default    | Description |
|---|------------|---|
| `--blockchain` | `ethereum` | Target blockchain network (e.g. `ethereum`, `bitcoin`, `tron`, `solana`) |
| `--start` | `0`        | Starting block height (set to `0` to calculate automatically via `--blocks`) |
| `--end` | `0`        | Ending block height (set to `0` for latest network block) |
| `--blocks` | `10`       | Number of blocks to look back if `--start` is `0` |
| `--limit` | `256`      | Maximum number of transactions to retrieve per page request |
| `--min-usd` | `100000`   | Minimum transaction value in USD to include in report |
| `--api-key` | `""`       | Optional API key override (defaults to `WHALE_ALERT_API_KEY` env var) |

Press `Ctrl+C` during a long scan to stop paginating and print the report for everything gathered so far.

---

## Sample Console Output

```text
2026/08/25 09:35:00 Initializing Historical Query for blockchain: ethereum
2026/08/25 09:35:00 Querying block height range: #20,590,001 to #20,590,100 (100 blocks) [Threshold: >= $100,000 USD]
2026/08/25 09:35:00 Fetching page 1...
2026/08/25 09:35:00 Processing page 1 (42 transactions received, blocks #20,590,001 to #20,590,067)...
2026/08/25 09:35:00 Fetching page 2...
2026/08/25 09:35:00 Processing page 2 (18 transactions received, blocks #20,590,068 to #20,590,100)...
2026/08/25 09:35:01 Fetching page 3...
2026/08/25 09:35:01 Page 3 returned 0 transactions. Historical scan complete.

===================================================================================
               WHALE ALERT HISTORICAL QUERY REPORT (ETHEREUM)
===================================================================================
Block Height Range  : #20,590,001 to #20,590,100 (100 blocks)
Time Span           : 2026-08-25 09:12:00 UTC to 2026-08-25 09:32:00 UTC
USD Threshold       : >= $100,000 USD
Scan Performance    : Processed 3 pages in 412ms
-----------------------------------------------------------------------------------
Total Transactions  : 60 transactions (64 sub-transactions)
Total Volume USD    : $482,150,900.50 USD
Unique Addresses    : 45 senders, 48 receivers

-----------------------------------------------------------------------------------
 VOLUME BREAKDOWN BY SYMBOL
-----------------------------------------------------------------------------------
SYMBOL     | TRANSFERS    | TOTAL AMOUNT             | TOTAL VOLUME (USD)
-----------+--------------+--------------------------+-----------------------------
USDT       | 28           | 210,000,000.0000         | $210,045,000.00
ETH        | 20           | 62,500.0000              | $187,500,000.00
USDC       | 16           | 84,500,000.0000          | $84,605,900.50

-----------------------------------------------------------------------------------
 TRANSACTION TYPES
-----------------------------------------------------------------------------------
 - Transfer     : 64

-----------------------------------------------------------------------------------
 TOP 5 HIGHEST VALUE TRANSFERS
-----------------------------------------------------------------------------------
#1 | Block #20590045 | 50,000,000.00 USDT ($50,012,500.00 USD)
    Type: transfer | From: binance         -> To: unknown         | Hash: 0x4a9b...7c1e
#2 | Block #20590082 | 15,000.00 ETH ($45,000,000.00 USD)
    Type: transfer | From: coinbase        -> To: kraken          | Hash: 0x8f12...3a99
===================================================================================
```
