# Python Quickstart: Wallet Watcher

[![Python Version](https://img.shields.io/badge/Python-3.10+-3776AB.svg?style=flat-square&logo=python)](https://www.python.org)
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

*(Alternatively, pass `--api-key "your_api_key_here"` or edit the `WHALE_ALERT_API_KEY` constant at the top of `main.py`)*

### 2. Install & Run
```bash
cd python/02_wallet_watcher
pip install -r requirements.txt
python main.py
```

---

## Command Line Flags

| Flag | Default | Description |
|---|---|---|
| `--api-key` | `""` | Optional API key override (defaults to `WHALE_ALERT_API_KEY` env var) |

All other settings are constants at the top of `main.py`:

```python
BLOCKCHAIN = "ethereum"
POLL_INTERVAL = 3  # seconds between polls

WATCHED_ADDRESS = "0x28c6c06298d514db089934071355e5743bf21d60"
WATCHED_LABEL = "Binance 14"
```

---

## How Direction Is Determined

Each sub-transaction is classified on its own, because one on-chain transaction can move several
assets at once. The watched address's inputs and outputs within that sub-transaction are summed and
compared:

| Condition | Direction | Reported amount |
|:---|:---|:---|
| inputs > outputs | `OUTGOING` | inputs − outputs (net of UTXO change) |
| outputs > inputs | `INCOMING` | outputs − inputs |
| inputs == outputs | `SELF-TRANSFER` | the moved amount |

An outgoing movement with no external recipients is reported as a `SELF-TRANSFER` only when the
watched address received something back — a self-sweep minus the network fee. Burns and locks have
no outputs at all, so nothing came back and they stay `OUTGOING`.

Sub-transactions where the watched address is listed but nothing moved are skipped. Freezes and
unfreezes look like that: they carry their magnitude in `balance` rather than `amount`, and
[example 03](../03_stablecoin_mints_burns/) reports them properly.

---

## Sample Console Output

```text
2026/08/24 13:20:00 Monitoring ethereum address 0x28c6c06298d514db089934071355e5743bf21d60 (Binance 14) from block #20589120...

[WATCHLIST MATCH] Binance 14 Wallet Activity
   Direction:   INCOMING
   Asset:       500.00 ETH ($1,750,000.00 USD)
   Price:       $3,500.00 USD / ETH
   From:        0x742d...f44e (Unknown)
   To:          0x28c6c06298d514db089934071355e5743bf21d60 (Binance 14)
   Tx Hash:     0x8e12...33fa
   Block:       #20589123
   Timestamp:   2026-08-24 13:20:05 UTC
--------------------------------------------------------------------------------
```
