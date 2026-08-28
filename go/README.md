# Whale Alert Go Examples

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8.svg?style=flat-square&logo=go)](https://go.dev)
[![Documentation](https://img.shields.io/badge/docs-whale--alert.io-blue.svg?style=flat-square)](https://docs.whale-alert.io)

Standalone Go quickstarts for the Whale Alert WebSocket and REST APIs. Each example is a
self-contained program with its own module — nothing is shared between them, so you can copy a
single directory out of this repository and it will still build.

---

## Prerequisites

- **Go 1.22 or newer** (`go version` to check). 1.22 is the floor declared in every `go.mod`
  and is verified in CI; newer versions work as well.
- **A Whale Alert API key** from the [Developer Portal](https://developer.whale-alert.io/api-account/login).
- Some examples require an **Enterprise** or **Enterprise Plus** plan — see the table below.

---

## Repository Layout

Every example directory is an independent Go module:

```
go/
├── 01_websocket_live_stream/   go.mod  go.sum  main.go  README.md
├── 02_wallet_watcher/          go.mod  go.sum  main.go  README.md
├── 03_stablecoin_mints_burns/  go.mod  go.sum  main.go  README.md
├── 04_historical_query/        go.mod  go.sum  main.go  README.md
├── 05_historical_balance/      go.mod  go.sum  main.go  README.md
└── 06_reorg_detector/          go.mod  go.sum  main.go  README.md
```

There is no workspace file and no root module. Always run commands from **inside** an example
directory; running `go build ./...` from the repository root will not find these modules.

Dependencies are minimal and pinned by the committed `go.sum` files:

| Module | Used by | Purpose |
|:---|:---|:---|
| `github.com/gorilla/websocket` | `01` | WebSocket client |
| `github.com/dustin/go-humanize` | all | Number and byte formatting in console output |

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

Each `main.go` also has a `whaleAlertApiKey` constant near the top, pre-set to
`YOUR_API_KEY_HERE`, which you can edit instead of setting the environment variable. Every
example also accepts an `-api-key` flag, which overrides both:

```bash
go run main.go -api-key "your_api_key_here"
```

Resolution order is the same in all six: `-api-key` flag → `WHALE_ALERT_API_KEY` environment
variable → `whaleAlertApiKey` constant.

If no key is found, every example exits immediately with a message rather than calling the API
with the placeholder value.

---

## Running an Example

```bash
cd go/01_websocket_live_stream
go run main.go
```

The first run downloads the two dependencies into your module cache; subsequent runs are
offline. To produce a binary instead:

```bash
cd go/01_websocket_live_stream
go build -o whale-stream .
./whale-stream
```

The long-running examples (`01`, `02`, `03`, `06`) stream until interrupted — press `Ctrl+C` for
a graceful shutdown. The query examples (`04`, `05`) run once and exit.

---

## Example Index

| # | Example | API | Plan | Configure via |
|:--|:---|:---|:---|:---|
| `01` | [WebSocket Live Stream](01_websocket_live_stream/) | WebSocket | All plans | Constants in `main.go` |
| `02` | [Wallet Watcher](02_wallet_watcher/) | REST | Enterprise / Plus | Constants in `main.go` |
| `03` | [Stablecoin Mints & Burns](03_stablecoin_mints_burns/) | REST | Enterprise / Plus | Constants in `main.go` |
| `04` | [Historical Query](04_historical_query/) | REST | Enterprise / Plus | Command-line flags |
| `05` | [Historical Balance](05_historical_balance/) | REST | Enterprise / Plus | Command-line flags |
| `06` | [Reorg Detector](06_reorg_detector/) | REST | Enterprise / Plus | Command-line flags |

Examples `01`–`03` take no flags beyond `-api-key`: change the subscription filter, watched
address, or USD threshold by editing the constants at the top of `main.go`. Examples `04`–`06`
are driven entirely by flags. In either case, `go run main.go -h` prints the full list, and each
example's README documents it in a table with defaults.

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

## Troubleshooting

**`WHALE_ALERT_API_KEY is not set`** — the environment variable is empty and the constant is
still `YOUR_API_KEY_HERE`. Note that `export` / `$env:` only applies to the current shell
session.

**`HTTP 401` or `403`** — the key is wrong, or the endpoint is outside your plan. Examples
`02`–`06` need Enterprise or Enterprise Plus.

**`HTTP 429 Too Many Requests`** — you are over your plan's rate limit. Examples `04`, `05`, and `06`
retry with exponential backoff automatically; for `02` and `03`, raise the `pollInterval`
constant.

**No output from a live example** — `01` defaults to a $100,000 USD minimum and `03` to $1,000;
lower the threshold, or widen the filters, if the chain is quiet.

**Empty historical results** — the block range may fall outside your plan's retention window.
Enterprise covers roughly the last 90 days; `GET /{blockchain}/status` reports the boundary as
`min_plan_height`. Example `05` checks this for you and reports it.

**`go: go.mod requires go >= …`** — your toolchain is older than 1.22. Upgrade Go, or set
`GOTOOLCHAIN=auto` to let Go fetch a newer one.

---

## Contributing

CI builds every example against Go 1.22 and current stable, runs `go vet`, and enforces
`gofmt`. Before opening a pull request:

```bash
gofmt -l go/
```

---

## Resources

- Documentation: [docs.whale-alert.io](https://docs.whale-alert.io)
- Live Supported Chains & Symbols: [leviathan.whale-alert.io/status?format=true](https://leviathan.whale-alert.io/status?format=true)
- Developer Portal: [developer.whale-alert.io/api-account/login](https://developer.whale-alert.io/api-account/login)
- Contact: [developers@whale-alert.io](mailto:developers@whale-alert.io) | [enterprise@whale-alert.io](mailto:enterprise@whale-alert.io)
