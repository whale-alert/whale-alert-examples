# Whale Alert Python Examples

[![Python Version](https://img.shields.io/badge/Python-3.10+-3776AB.svg?style=flat-square&logo=python)](https://www.python.org)
[![Documentation](https://img.shields.io/badge/docs-whale--alert.io-blue.svg?style=flat-square)](https://docs.whale-alert.io)

Standalone Python quickstarts for the Whale Alert WebSocket and REST APIs. Each example is a
single self-contained `main.py` with its own `requirements.txt` — nothing is shared between them,
so you can copy a single directory out of this repository and it will still run.

---

## Prerequisites

- **Python 3.10 or newer** (`python --version` to check).
- **A Whale Alert API key** from the [Developer Portal](https://developer.whale-alert.io/api-account/login).
- Some examples require an **Enterprise** or **Enterprise Plus** plan — see the table below.

---

## Repository Layout

```
python/
├── 01_websocket_live_stream/  main.py  requirements.txt  README.md
├── 02_wallet_watcher/         main.py  requirements.txt  README.md
├── 03_stablecoin_mints_burns/ main.py  requirements.txt  README.md
├── 04_historical_query/       main.py  requirements.txt  README.md
├── 05_historical_balance/     main.py  requirements.txt  README.md
└── 06_reorg_detector/         main.py  requirements.txt  README.md
```

There is no shared package and no root `requirements.txt`. Always run commands from **inside** an
example directory.

Dependencies are minimal — everything else comes from the standard library:

| Package | Used by | Purpose |
|:---|:---|:---|
| `websockets` | `01` | Asynchronous WebSocket client |
| `requests` | `02`–`06` | HTTP client for the REST API |

Number formatting uses Python's built-in format specifiers (`f"{value:,.2f}"`), so no formatting
library is needed.

---

## Installing Dependencies

A virtual environment keeps these packages out of your system Python:

**Linux / macOS**:
```bash
python -m venv .venv && source .venv/bin/activate
```

**Windows (PowerShell)**:
```powershell
python -m venv .venv; .\.venv\Scripts\Activate.ps1
```

Then, from inside an example directory:

```bash
pip install -r requirements.txt
```

---

## Setting Your API Key

All examples read the `WHALE_ALERT_API_KEY` environment variable.

**Linux / macOS**:
```bash
export WHALE_ALERT_API_KEY="your_api_key_here"
```

**Windows (PowerShell)**:
```powershell
$env:WHALE_ALERT_API_KEY="your_api_key_here"
```

Each `main.py` also has a `WHALE_ALERT_API_KEY` constant near the top, pre-set to
`YOUR_API_KEY_HERE`, which you can edit instead of setting the environment variable. Every
example also accepts an `--api-key` flag, which overrides both:

```bash
python main.py --api-key "your_api_key_here"
```

Resolution order is the same in all six: `--api-key` flag → `WHALE_ALERT_API_KEY` environment
variable → `WHALE_ALERT_API_KEY` constant.

If no key is found, every example exits immediately with a message rather than calling the API
with the placeholder value.

---

## Running an Example

```bash
cd python/01_websocket_live_stream
pip install -r requirements.txt
python main.py
```

The long-running examples (`01`, `02`, `03`, `06`) stream until interrupted — press `Ctrl+C` for
a graceful shutdown. The query examples (`04`, `05`) run once and exit.

---

## Example Index

| # | Example | API | Plan | Configure via |
|:--|:---|:---|:---|:---|
| `01` | [WebSocket Live Stream](01_websocket_live_stream/) | WebSocket | All plans | Constants in `main.py` |
| `02` | [Wallet Watcher](02_wallet_watcher/) | REST | Enterprise / Plus | Constants in `main.py` |
| `03` | [Stablecoin Mints & Burns](03_stablecoin_mints_burns/) | REST | Enterprise / Plus | Constants in `main.py` |
| `04` | [Historical Query](04_historical_query/) | REST | Enterprise / Plus | Command-line flags |
| `05` | [Historical Balance](05_historical_balance/) | REST | Enterprise / Plus | Command-line flags |
| `06` | [Reorg Detector](06_reorg_detector/) | REST | Enterprise / Plus | Command-line flags |

Examples `01`–`03` take no flags beyond `--api-key`: change the subscription filter, watched
address, or USD threshold by editing the constants at the top of `main.py`. Examples `04`–`06`
are driven entirely by flags. In either case, `python main.py -h` prints the full list, and each
example's README documents it in a table with defaults.

A Go version of each of these exists in [`../go`](../go/).

### Endpoints used

| Example | Endpoints |
|:---|:---|
| `01` | `wss://leviathan.whale-alert.io/ws` |
| `02` | `GET /{blockchain}/status`, `GET /{blockchain}/transactions` |
| `03` | `GET /status`, `GET /{blockchain}/status`, `GET /{blockchain}/transactions` |
| `04` | `GET /{blockchain}/status`, `GET /{blockchain}/transactions` |
| `05` | `GET /{blockchain}/height_at_time/{timestamp}`, `GET /{blockchain}/status`, `GET /{blockchain}/transactions` |
| `06` | `GET /{blockchain}/status`, `GET /{blockchain}/block/{height}` |

REST base URL: `https://leviathan.whale-alert.io`. For the request and response reference, see
[docs.whale-alert.io](https://docs.whale-alert.io); for the transaction schema shared by all of
these examples, see the [Data Model](../README.md#data-model) section of the root README.

---

## Working With the Data

Amounts and balances arrive as decimal **strings** so that no precision is lost on large or
high-decimal values. Every example converts them explicitly with `float()` before doing
arithmetic, which is enough for the reporting these examples do. If you are settling real
balances rather than printing them, parse with `decimal.Decimal` instead:

```python
from decimal import Decimal

amount = Decimal(account["amount"])          # exact
value_usd = amount * Decimal(str(sub["unit_price_usd"]))
```

`unit_price_usd` is the USD price of a **single unit** of the asset at the block timestamp:
multiply it by `amount` for the USD value of a movement, or by `balance` for the USD value of an
address holding.

---

## Troubleshooting

**`WHALE_ALERT_API_KEY is not set`** — the environment variable is empty and the constant is
still `YOUR_API_KEY_HERE`. Note that `export` / `$env:` only applies to the current shell
session.

**`ModuleNotFoundError: No module named 'websockets'`** (or `'requests'`) — install the example's
dependencies with `pip install -r requirements.txt` from inside its directory, and check that the
virtual environment you installed into is the one that is active.

**`HTTP 401` or `403`** — the key is wrong, or the endpoint is outside your plan. Examples
`02`–`06` need Enterprise or Enterprise Plus.

**`HTTP 429 Too Many Requests`** — you are over your plan's rate limit. Examples `04`, `05`, and
`06` retry with exponential backoff automatically; for `02` and `03`, raise the `POLL_INTERVAL`
constant. Example `06` is the heaviest: raise its `--poll` as well.

**No output from a live example** — `01` defaults to a $100,000 USD minimum and `03` to $1,000;
lower the threshold, or widen the filters, if the chain is quiet.

**Empty historical results** — the block range may fall outside your plan's retention window.
Enterprise covers roughly the last 90 days; `GET /{blockchain}/status` reports the boundary as
`min_plan_height`. Example `05` checks this for you and reports it.

---

## Contributing

CI byte-compiles every example on Python 3.10 and current stable, and installs each
`requirements.txt` to confirm the imports resolve. Before opening a pull request:

```bash
python -m compileall -q python/
```

---

## Resources

- Documentation: [docs.whale-alert.io](https://docs.whale-alert.io)
- Live Supported Chains & Symbols: [leviathan.whale-alert.io/status?format=true](https://leviathan.whale-alert.io/status?format=true)
- Developer Portal: [developer.whale-alert.io/api-account/login](https://developer.whale-alert.io/api-account/login)
- Contact: [developers@whale-alert.io](mailto:developers@whale-alert.io) | [enterprise@whale-alert.io](mailto:enterprise@whale-alert.io)
