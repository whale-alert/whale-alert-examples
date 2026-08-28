# Python Quickstart: Stablecoin Liquidity & Supply Events (Mints, Burns, Freezes & Locks)

[![Python Version](https://img.shields.io/badge/Python-3.10+-3776AB.svg?style=flat-square&logo=python)](https://www.python.org)
[![API Plan: Enterprise](https://img.shields.io/badge/API%20Plan-Enterprise%20%7C%20Enterprise%20Plus-orange.svg?style=flat-square)](https://whale-alert.io)

Track real-time supply changes and governance actions across 23 major stablecoins (including USDT, USDC, DAI, USDS, PYUSD, and RLUSD) using the Whale Alert REST API.

---

## Overview

1. **Dynamic Blockchain Discovery**: Queries the global status endpoint (`GET /status`) at startup to discover active blockchains and identify networks supporting tracked stablecoins.
2. **Real-Time Cursor Polling**: Spawns one worker thread per blockchain, each polling that chain's transactions endpoint (`GET /{blockchain}/transactions?start_height={height}`) using the `next` pagination cursor.
3. **Multi-Event Classification**: Classifies liquidity movements into **Mints**, **Burns**, **Freezes**, **Unfreezes**, **Locks**, and **Unlocks**.
4. **Threshold Filtering**: Alerts on transactions exceeding a configurable USD value (default: `$1,000 USD`).

---

## Supported Event Types Explained

| Event | Operation | Description | Impact on Supply / Circulation |
|---|---|---|---|
| **Mint** | Token Creation | New tokens issued by the protocol or treasury (e.g. Tether Treasury, Circle) to an authorized recipient or treasury wallet. | **Increases circulating supply** |
| **Burn** | Token Destruction | Tokens permanently destroyed or redeemed back into the issuer's burn/null address (`0x0...`, `0xdead...`). | **Decreases circulating supply** |
| **Freeze** | Address Blacklisting | Centralized issuers (e.g. Tether or Circle) blacklist a wallet address due to regulatory compliance, sanctions, or theft, disabling transfers. | **Restricts liquid circulation** |
| **Unfreeze** | Address Whitelisting | The issuer lifts a previous blacklist or freeze on a wallet address, restoring full transfer permissions. | **Restores liquid circulation** |
| **Lock** | Reserve / Vault Escrow | Tokens deposited into a smart contract vault, time-lock, MakerDAO DSR/escrow, or bridge collateral pool. | **Temporarily locks liquidity** |
| **Unlock** | Escrow Release | Tokens released from a smart contract time-lock, staking pool, or bridge reserve back into active circulation. | **Releases liquid circulation** |

Freeze and unfreeze events report the affected address's **balance** rather than a transferred amount, since no tokens move.

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
cd python/03_stablecoin_mints_burns
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

## Sample Console Output

```text
[2026-08-25 08:50:00] Stablecoin Mint, Burn, Freeze & Lock Monitor active (Threshold: $1,000 USD)...
2026/08/25 08:50:01 Discovered 4 blockchains supporting stablecoins from /status: ethereum, bitcoin, tron, solana
2026/08/25 08:50:01 [Ethereum] Monitoring from block #20589120...
2026/08/25 08:50:01 [Tron] Monitoring from block #63892110...

[STABLECOIN MINT]
   Asset:       100,000,000 USDT ($100,000,000.00 USD)
   Blockchain:  Tron
   To:          Tether Treasury
   Hash:        3a9b...7c1e

[STABLECOIN BURN]
   Asset:       50,000,000 USDC ($50,000,000.00 USD)
   Blockchain:  Ethereum
   From:        Circle
   Hash:        0x8f12...44e1

[STABLECOIN FREEZE]
   Balance:     10,000,000 USDT ($10,000,000.00 USD)
   Blockchain:  Ethereum
   Target:      0x3f5c...f0be
   Hash:        0x4a9b...7c1e

[STABLECOIN UNFREEZE]
   Balance:     10,000,000 USDT ($10,000,000.00 USD)
   Blockchain:  Ethereum
   Target:      0x3f5c...f0be
   Hash:        0x4a9b...7c1e

[STABLECOIN LOCK]
   Asset:       25,000,000 DAI ($25,000,000.00 USD)
   Blockchain:  Ethereum
   From:        0x1a2b...3c4d
   Locked At:   0x3732...1ddb (MakerDAO DSR Contract)
   Hash:        0x7b1c...99a2

[STABLECOIN UNLOCK]
   Asset:       25,000,000 DAI ($25,000,000.00 USD)
   Blockchain:  Ethereum
   To:          0x1a2b...3c4d
   Hash:        0x7b1c...99a2
```

---

## Configuration

Customize the USD threshold and supported stablecoins directly in `main.py`:

```python
# Minimum USD threshold for alerts ($1,000 USD default)
MIN_USD_THRESHOLD = 1_000.0

# Supported tracked stablecoins (lowercase symbol -> token name)
STABLECOINS = {
    "usdt": "Tether",
    "usdc": "USD Coin",
    "dai": "Dai",
    "fei": "Fei USD",
    "pax": "Paxos Standard",
    "ust": "TerraUSD",
    "busd": "Binance USD",
    "eurt": "Tether EUR",
    "gusd": "Gemini Dollar",
    "husd": "HUSD",
    "pusd": "PUSD",
    "tusd": "TrueUSD",
    "usdd": "Decentralized USD",
    "usde": "Ethena USDe",
    "usdg": "Global Dollar",
    "usdh": "USDH",
    "usdj": "JUST Stablecoin",
    "usds": "Sky Dollar",
    "usd1": "USD1",
    "pyusd": "PayPal USD",
    "rlusd": "Ripple USD",
    "susds": "Savings USDS",
    "pathusd": "PathUSD",
}
```
