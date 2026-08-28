"""Whale Alert Quickstart: Historical Query (Block Range).

Queries historical transactions over a block height range, follows the API's cursor
pagination to the end of the range, and prints an aggregated report: total USD volume,
per-symbol breakdown, transaction type distribution, and the top transfers.
"""

import argparse
import logging
import os
import sys
import time
from datetime import datetime, timezone
from urllib.parse import urlencode, urlparse, urlunparse, parse_qs

import requests

# Whale Alert API Key.
# Replace this value or set the WHALE_ALERT_API_KEY environment variable.
WHALE_ALERT_API_KEY = "YOUR_API_KEY_HERE"

API_BASE_URL = "https://leviathan.whale-alert.io"
DEFAULT_BLOCKCHAIN = "ethereum"
DEFAULT_BLOCK_RANGE = 10
DEFAULT_LIMIT = 256
DEFAULT_MIN_USD = 100_000.0

HTTP_TIMEOUT = 15  # seconds
MAX_RETRIES = 3
INITIAL_BACKOFF = 1  # seconds

logging.basicConfig(format="%(asctime)s %(message)s", datefmt="%Y/%m/%d %H:%M:%S", level=logging.INFO)
log = logging.getLogger(__name__)

session = requests.Session()

REPORT_WIDTH = 83


def resolve_api_key(flag_key: str) -> str:
    """Inspect CLI flags, environment variables, and fallback constants in priority order."""
    if flag_key:
        return flag_key
    return os.environ.get("WHALE_ALERT_API_KEY", "") or WHALE_ALERT_API_KEY


def fetch_json(request_url: str, api_key: str):
    """Execute an HTTP GET request, inject the API key parameter, handle retry backoff
    for rate limits (HTTP 429) and server errors (HTTP 5xx), and decode the JSON payload.
    """
    if not request_url.startswith("http"):
        request_url = API_BASE_URL + request_url

    parsed = urlparse(request_url)
    if api_key and "api_key" not in parse_qs(parsed.query):
        query = parse_qs(parsed.query)
        query["api_key"] = [api_key]
        parsed = parsed._replace(query=urlencode(query, doseq=True))
    request_url = urlunparse(parsed)

    last_error = None
    backoff = INITIAL_BACKOFF

    for attempt in range(1, MAX_RETRIES + 1):
        try:
            response = session.get(request_url, timeout=HTTP_TIMEOUT)
        except requests.RequestException as exc:
            last_error = exc
        else:
            if response.status_code == 200:
                return response.json()

            body = response.text.strip()
            status = f"HTTP {response.status_code} ({response.reason})"
            last_error = RuntimeError(f"{status}: {body}" if body else status)

            # Fatal client errors (400 Bad Request, 401 Unauthorized, 403 Forbidden) are not retryable
            if response.status_code != 429 and response.status_code < 500:
                raise last_error

            # Only announce a retry when one is actually coming.
            if attempt < MAX_RETRIES:
                if response.status_code == 429:
                    log.info(
                        "Rate limit encountered (HTTP 429). Retrying attempt %d/%d in %ds...",
                        attempt, MAX_RETRIES, backoff,
                    )
                else:
                    log.info(
                        "Server error (HTTP %d). Retrying attempt %d/%d in %ds...",
                        response.status_code, attempt, MAX_RETRIES, backoff,
                    )

        # Only back off between attempts. Sleeping after the final attempt just
        # delays the error the caller is already going to get.
        if attempt < MAX_RETRIES:
            time.sleep(backoff)
            backoff *= 2

    raise RuntimeError(f"request failed after {MAX_RETRIES} retries: {last_error}")


def get_latest_height(chain: str, api_key: str) -> int:
    """Fetch the newest confirmed block height from GET /{blockchain}/status."""
    status = fetch_json(f"/{chain}/status", api_key)
    return int(status.get("end_height", 0))


def sum_amounts(accounts: list) -> float:
    """Parse and sum positive float amounts from a list of accounts."""
    total = 0.0
    for account in accounts:
        try:
            amount = float(account.get("amount", 0) or 0)
        except (TypeError, ValueError):
            continue
        if amount > 0:
            total += amount
    return total


def extract_party(accounts: list, address_set: set) -> str:
    """Record unique addresses into the set and return the primary entity owner label if known."""
    owner = "unknown"
    for account in accounts:
        address = account.get("address")
        if address:
            address_set.add(address)
        if account.get("owner") and owner == "unknown":
            owner = account["owner"]
    return owner


class AggregatedMetrics:
    """Holds all historical metrics collected across the queried block height range."""

    def __init__(self, start_block: int, end_block: int):
        self.start_block = start_block
        self.end_block = end_block
        self.min_timestamp = 0
        self.max_timestamp = 0
        self.total_tx_count = 0
        self.total_sub_tx_count = 0
        self.total_volume_usd = 0.0
        # symbol -> {"count": int, "total_amount": float, "volume_usd": float}
        self.symbol_stats: dict = {}
        self.type_stats: dict = {}
        self.unique_senders: set = set()
        self.unique_receivers: set = set()
        self.top_transfers: list = []


def process_transactions(transactions: list, metrics: AggregatedMetrics, min_usd: float) -> None:
    """Filter and aggregate a batch of transactions into statistical metrics.

    API Understanding:
      - A single on-chain transaction can encompass multiple sub-transactions
        (e.g. multi-token contract interactions).
      - Each sub-transaction represents an individual asset movement with its own symbol,
        unit price, inputs, and outputs.
      - Amounts are provided as strings in JSON to preserve precision across varying token decimals.
      - For standard transfers/mints, transferred value is in `outputs`. For `burn` events where tokens
        are destroyed without destination outputs, we fall back to summing `inputs`.
    """
    for transaction in transactions:
        has_matching_sub_tx = False

        for sub in transaction.get("sub_transactions") or []:
            symbol = str(sub.get("symbol", "")).upper()
            tx_type = str(sub.get("transaction_type", "")).lower()

            # Parse token amount: check outputs first, fall back to inputs for burns
            amount = sum_amounts(sub.get("outputs") or []) or sum_amounts(sub.get("inputs") or [])
            if amount <= 0:
                continue

            # Calculate USD fiat valuation using the unit price at the time of the transaction
            try:
                unit_price = float(sub.get("unit_price_usd", 0) or 0)
            except (TypeError, ValueError):
                unit_price = 0.0
            amount_usd = amount * unit_price if unit_price > 0 else 0.0

            # Filter out transactions below the USD threshold to focus on high-value whale movements
            if amount_usd < min_usd:
                continue

            has_matching_sub_tx = True
            metrics.total_sub_tx_count += 1
            metrics.type_stats[tx_type] = metrics.type_stats.get(tx_type, 0) + 1

            # Track volume and count per cryptocurrency symbol
            stat = metrics.symbol_stats.setdefault(symbol, {"count": 0, "total_amount": 0.0, "volume_usd": 0.0})
            stat["count"] += 1
            stat["total_amount"] += amount
            stat["volume_usd"] += amount_usd
            metrics.total_volume_usd += amount_usd

            # Deduplicate unique addresses and extract known entity owner labels (e.g. "binance", "coinbase")
            from_owner = extract_party(sub.get("inputs") or [], metrics.unique_senders)
            to_owner = extract_party(sub.get("outputs") or [], metrics.unique_receivers)

            # Record qualifying transfer for top-value ranking
            metrics.top_transfers.append(
                {
                    "hash": transaction.get("hash", ""),
                    "block_height": transaction.get("height", 0),
                    "timestamp": transaction.get("timestamp", 0),
                    "symbol": symbol,
                    "amount": amount,
                    "amount_usd": amount_usd,
                    "type": tx_type,
                    "from_owner": from_owner,
                    "to_owner": to_owner,
                }
            )

        # If at least one sub-transaction qualified, update parent transaction count and timestamp window
        if has_matching_sub_tx:
            metrics.total_tx_count += 1
            timestamp = transaction.get("timestamp", 0) or 0
            if metrics.min_timestamp == 0 or timestamp < metrics.min_timestamp:
                metrics.min_timestamp = timestamp
            if timestamp > metrics.max_timestamp:
                metrics.max_timestamp = timestamp


def format_short_hash(tx_hash: str) -> str:
    """Truncate 64-character transaction hashes for clean console tabular display."""
    if len(tx_hash) > 16:
        return f"{tx_hash[:8]}...{tx_hash[-8:]}"
    return tx_hash


def format_utc(unix_ts: int) -> str:
    """Render a UNIX timestamp as a UTC datetime string."""
    return datetime.fromtimestamp(unix_ts, tz=timezone.utc).strftime("%Y-%m-%d %H:%M:%S UTC")


def print_report(chain: str, metrics: AggregatedMetrics, min_usd: float, duration: float, pages: int) -> None:
    """Display a clean, formatted ASCII report summarizing the historical scan results."""
    print()
    print("=" * REPORT_WIDTH)
    print(f"               WHALE ALERT HISTORICAL QUERY REPORT ({chain.upper()})")
    print("=" * REPORT_WIDTH)
    block_count = metrics.end_block - metrics.start_block + 1
    print(f"Block Height Range  : #{metrics.start_block:,} to #{metrics.end_block:,} ({block_count:,} blocks)")

    if metrics.min_timestamp > 0 and metrics.max_timestamp > 0:
        print(f"Time Span           : {format_utc(metrics.min_timestamp)} to {format_utc(metrics.max_timestamp)}")

    print(f"USD Threshold       : >= ${min_usd:,.0f} USD")
    print(f"Scan Performance    : Processed {pages} pages in {duration * 1000:.0f}ms")
    print("-" * REPORT_WIDTH)
    print(
        f"Total Transactions  : {metrics.total_tx_count:,} transactions "
        f"({metrics.total_sub_tx_count:,} sub-transactions)"
    )
    print(f"Total Volume USD    : ${metrics.total_volume_usd:,.2f} USD")
    print(f"Unique Addresses    : {len(metrics.unique_senders):,} senders, {len(metrics.unique_receivers):,} receivers")

    # 1. Volume Breakdown by Symbol
    if metrics.symbol_stats:
        print()
        print("-" * REPORT_WIDTH)
        print(" VOLUME BREAKDOWN BY SYMBOL")
        print("-" * REPORT_WIDTH)
        print(f"{'SYMBOL':<10} | {'TRANSFERS':<12} | {'TOTAL AMOUNT':<24} | {'TOTAL VOLUME (USD)':<24}")
        print("-" * 11 + "+" + "-" * 14 + "+" + "-" * 26 + "+" + "-" * 29)

        for symbol, stat in sorted(metrics.symbol_stats.items(), key=lambda item: item[1]["volume_usd"], reverse=True):
            print(
                f"{symbol:<10} | {stat['count']:<12,} | {stat['total_amount']:<24,.4f} | ${stat['volume_usd']:,.2f}"
            )

    # 2. Breakdown by Transaction Type
    if metrics.type_stats:
        print()
        print("-" * REPORT_WIDTH)
        print(" TRANSACTION TYPES")
        print("-" * REPORT_WIDTH)
        # Most frequent first; ties broken alphabetically.
        for tx_type, count in sorted(metrics.type_stats.items(), key=lambda item: (-item[1], item[0])):
            print(f" - {tx_type.capitalize():<12} : {count:,}")

    # 3. Top 5 Highest Value Transfers
    if metrics.top_transfers:
        metrics.top_transfers.sort(key=lambda transfer: transfer["amount_usd"], reverse=True)
        top_n = min(len(metrics.top_transfers), 5)

        print()
        print("-" * REPORT_WIDTH)
        print(f" TOP {top_n} HIGHEST VALUE TRANSFERS")
        print("-" * REPORT_WIDTH)
        for index in range(top_n):
            transfer = metrics.top_transfers[index]
            print(
                f"#{index + 1} | Block #{transfer['block_height']} | "
                f"{transfer['amount']:,.2f} {transfer['symbol']} (${transfer['amount_usd']:,.2f} USD)"
            )
            print(
                f"    Type: {transfer['type']:<8} | From: {transfer['from_owner']:<15} -> "
                f"To: {transfer['to_owner']:<15} | Hash: {format_short_hash(transfer['hash'])}"
            )

    print("=" * REPORT_WIDTH)


def main() -> None:
    # 1. Parse command-line flags
    parser = argparse.ArgumentParser(description="Query and aggregate historical Whale Alert transactions.")
    parser.add_argument("--blockchain", default=DEFAULT_BLOCKCHAIN,
                        help="Target blockchain (e.g. ethereum, bitcoin, tron)")
    parser.add_argument("--start", type=int, default=0,
                        help="Starting block height (0 to calculate using --blocks offset)")
    parser.add_argument("--end", type=int, default=0,
                        help="Ending block height (0 for latest network block)")
    parser.add_argument("--blocks", type=int, default=DEFAULT_BLOCK_RANGE,
                        help="Block height range count (used if --start is 0)")
    parser.add_argument("--limit", type=int, default=DEFAULT_LIMIT,
                        help="Maximum number of transactions to retrieve per page")
    parser.add_argument("--min-usd", type=float, default=DEFAULT_MIN_USD,
                        help="Minimum transaction value in USD to include in analysis")
    parser.add_argument("--api-key", default="",
                        help="Whale Alert API key (defaults to WHALE_ALERT_API_KEY env var)")
    args = parser.parse_args()

    api_key = resolve_api_key(args.api_key)
    if not api_key or api_key == "YOUR_API_KEY_HERE":
        log.error("Warning: WHALE_ALERT_API_KEY is not set. REST historical queries require an active API key.")
        sys.exit(1)

    chain = args.blockchain.lower()
    log.info("Initializing Historical Query for blockchain: %s", chain)

    # 2. Resolve block height range:
    # If --end is not specified, query GET /{blockchain}/status for the latest confirmed block height.
    end_block = args.end
    if end_block == 0:
        try:
            end_block = get_latest_height(chain, api_key)
        except (requests.RequestException, RuntimeError, ValueError) as exc:
            log.error("Failed to fetch latest block height for %s: %s", chain, exc)
            sys.exit(1)

    # If --start is not specified, compute it as (end_block - blocks + 1)
    start_block = args.start
    if start_block == 0:
        start_block = end_block - args.blocks + 1 if end_block >= args.blocks else 1

    if start_block > end_block:
        log.error(
            "Invalid block height range: start_height (#%d) is greater than end_height (#%d)", start_block, end_block
        )
        sys.exit(1)

    log.info(
        "Querying block height range: #%s to #%s (%s blocks) [Threshold: >= $%s USD]",
        f"{start_block:,}", f"{end_block:,}", f"{end_block - start_block + 1:,}", f"{args.min_usd:,.0f}",
    )

    metrics = AggregatedMetrics(start_block, end_block)
    limit = args.limit if args.limit > 0 else DEFAULT_LIMIT

    # 3. Construct initial REST query URL:
    # GET /{blockchain}/transactions?start_height=...&end_height=...&min_value=...&limit=...
    current_url = (
        f"{API_BASE_URL}/{chain}/transactions"
        f"?start_height={start_block}&end_height={end_block}&min_value={int(args.min_usd)}&limit={limit}"
    )

    start_time = time.monotonic()
    page_count = 0

    # 4. Cursor-based pagination loop:
    # The Whale Alert API returns a `next` URL string containing the pagination cursor.
    # When `next` is empty or matches current_url, the scan across the block range is complete.
    try:
        while current_url:
            page_count += 1
            log.info("Fetching page %d...", page_count)

            try:
                response = fetch_json(current_url, api_key)
            except (requests.RequestException, RuntimeError, ValueError) as exc:
                log.error("Error fetching transaction page %d: %s", page_count, exc)
                break

            transactions = response.get("transactions") or []
            if not transactions:
                log.info("Page %d returned 0 transactions. Historical scan complete.", page_count)
                break

            min_block = transactions[0].get("height", 0)
            max_block = transactions[-1].get("height", 0)
            log.info(
                "Processing page %d (%d transactions received, blocks #%s to #%s)...",
                page_count, len(transactions), f"{min_block:,}", f"{max_block:,}",
            )

            process_transactions(transactions, metrics, args.min_usd)

            next_url = response.get("next") or ""
            current_url = next_url if next_url and next_url != current_url else ""
    except KeyboardInterrupt:
        log.info("Cancellation signal received. Reporting on what was scanned so far...")

    duration = time.monotonic() - start_time

    # 5. Display comprehensive historical summary report
    print_report(chain, metrics, args.min_usd, duration, page_count)


if __name__ == "__main__":
    main()
