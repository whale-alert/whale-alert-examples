"""Whale Alert Quickstart: Wallet Watcher.

Polls the Whale Alert REST API for transactions involving a single watched address and
prints every match, classified as incoming, outgoing, or a self-transfer.
"""

import argparse
import logging
import os
import signal
import sys
import threading
from datetime import datetime, timezone
from urllib.parse import quote, urlencode, urlparse, urlunparse, parse_qs

import requests

# Whale Alert API Key.
# Replace this value or set the WHALE_ALERT_API_KEY environment variable.
WHALE_ALERT_API_KEY = "YOUR_API_KEY_HERE"

API_BASE_URL = "https://leviathan.whale-alert.io"
BLOCKCHAIN = "ethereum"
POLL_INTERVAL = 3  # seconds between polls
HTTP_TIMEOUT = 15  # seconds
RETRY_DELAY = 5  # seconds to wait after a failed request

# Address to monitor and its friendly display label
WATCHED_ADDRESS = "0x28c6c06298d514db089934071355e5743bf21d60"
WATCHED_LABEL = "Binance 14"

logging.basicConfig(format="%(asctime)s %(message)s", datefmt="%Y/%m/%d %H:%M:%S", level=logging.INFO)
log = logging.getLogger(__name__)

DIVIDER = "-" * 80

# HTTP session reused across polls for connection pooling
session = requests.Session()

# Set on SIGINT / SIGTERM to stop the polling loop
shutdown = threading.Event()


def get_api_key(flag_key: str) -> str:
    """Resolve the API key from the --api-key flag, then WHALE_ALERT_API_KEY, then the constant."""
    if flag_key:
        return flag_key

    env_key = os.environ.get("WHALE_ALERT_API_KEY", "")
    if env_key:
        return env_key

    if WHALE_ALERT_API_KEY == "YOUR_API_KEY_HERE":
        log.error(
            "Warning: WHALE_ALERT_API_KEY is not set. Please set the WHALE_ALERT_API_KEY "
            "environment variable, pass --api-key, or edit the constant in main.py."
        )
        sys.exit(1)

    return WHALE_ALERT_API_KEY


def fetch_json(request_url: str, api_key: str) -> dict:
    """Execute an HTTP GET request, append the API key, and decode the JSON payload."""
    parsed = urlparse(request_url)
    if api_key and "api_key" not in parse_qs(parsed.query):
        query = parse_qs(parsed.query)
        query["api_key"] = [api_key]
        parsed = parsed._replace(query=urlencode(query, doseq=True))

    response = session.get(urlunparse(parsed), timeout=HTTP_TIMEOUT)
    if response.status_code != 200:
        body = response.text.strip()
        raise RuntimeError(f"HTTP {response.status_code}: {body}" if body else f"HTTP {response.status_code}")

    return response.json()


def get_latest_height(chain: str, api_key: str) -> int:
    """Fetch the newest block height from the status endpoint."""
    status = fetch_json(f"{API_BASE_URL}/{chain}/status", api_key)
    return int(status.get("end_height", 0))


def fetch_transactions(fetch_url: str, api_key: str) -> dict:
    """Retrieve transactions from the given URL and parse the response.

    The `next` cursor may come back as a relative path, so absolutize it first.
    """
    if not fetch_url.startswith(("http://", "https://")):
        if not fetch_url.startswith("/"):
            fetch_url = "/" + fetch_url
        fetch_url = API_BASE_URL + fetch_url

    return fetch_json(fetch_url, api_key)


def filter_accounts(accounts: list, target_address: str, match: bool) -> list:
    """Return accounts matching (match=True) or excluding (match=False) the target address.

    Compare case-insensitively: EVM addresses are commonly returned in
    EIP-55 checksummed (mixed-case) form.
    """
    target = target_address.lower()
    return [acc for acc in accounts if (str(acc.get("address", "")).lower() == target) == match]


def sum_amounts(accounts: list) -> float:
    """Sum parsed float amounts for a list of accounts."""
    total = 0.0
    for acc in accounts:
        try:
            amount = float(acc.get("amount", 0))
        except (TypeError, ValueError):
            continue
        if amount > 0:
            total += amount
    return total


def format_amount(amount: float) -> str:
    """Format numbers with suitable precision (comma-separated for >= 1, up to 8 decimals for < 1)."""
    if amount <= 0:
        return "0.00"
    if amount >= 1.0:
        return f"{amount:,.2f}"
    return f"{amount:.8f}".rstrip("0").rstrip(".")


def shorten_address(address: str) -> str:
    """Truncate long addresses for clean console output."""
    address = address.strip()
    if len(address) > 14:
        return f"{address[:6]}...{address[-4:]}"
    return address


def format_party(account: dict) -> str:
    """Format an individual account with its label or owner."""
    address = str(account.get("address", ""))
    if address.lower() == WATCHED_ADDRESS.lower():
        return f"{address} ({WATCHED_LABEL})"

    short_address = shorten_address(address)
    owner = str(account.get("owner", ""))
    if owner and owner.lower() != "unknown":
        owner_type = str(account.get("owner_type", ""))
        if owner_type and owner_type.lower() != "unknown":
            owner_description = f"{owner} [{owner_type.capitalize()}]"
        else:
            owner_description = owner
        return f"{short_address} ({owner_description})" if short_address else owner_description

    return f"{short_address} (Unknown)" if short_address else "Unknown"


def format_parties(accounts: list) -> str:
    """Format a list of accounts with their labels/owners, deduplicating identical entries."""
    parts = []
    for account in accounts:
        formatted = format_party(account)
        if formatted not in parts:
            parts.append(formatted)
    return ", ".join(parts) if parts else "Unknown"


def check_transaction(transaction: dict) -> None:
    """Inspect a transaction's sub-transactions for activity involving the watched address."""
    # A blockchain transaction can contain multiple sub-transactions (e.g., token swaps, batch transfers).
    # We evaluate each sub-transaction independently because each has its own asset symbol, unit price,
    # inputs, and outputs.
    for sub in transaction.get("sub_transactions") or []:
        inputs = sub.get("inputs") or []
        outputs = sub.get("outputs") or []

        watched_inputs = filter_accounts(inputs, WATCHED_ADDRESS, True)
        watched_outputs = filter_accounts(outputs, WATCHED_ADDRESS, True)

        # Skip if the watched address had no role in this specific sub-transaction
        if not watched_inputs and not watched_outputs:
            continue

        in_amount = sum_amounts(watched_inputs)
        out_amount = sum_amounts(watched_outputs)

        # The address is listed but nothing moved. Freezes and unfreezes look like this:
        # they carry their magnitude in `balance`, not `amount`. Reporting them here would
        # print "0.00" with no information; example 03 covers those events properly.
        if in_amount == 0 and out_amount == 0:
            continue

        other_inputs = filter_accounts(inputs, WATCHED_ADDRESS, False)
        other_outputs = filter_accounts(outputs, WATCHED_ADDRESS, False)

        if in_amount > out_amount:
            # Spent more than received back (UTXO change) -> Net OUTGOING
            direction = "OUTGOING"
            amount = in_amount - out_amount
            from_accounts = watched_inputs
            to_accounts = other_outputs

            # No external recipients. A sweep back to the watched address itself, minus the
            # network fee, is a self-transfer. Burns and locks have no outputs at all, so
            # nothing came back and the movement stays OUTGOING.
            if not to_accounts and watched_outputs:
                direction = "SELF-TRANSFER"
                amount = out_amount or in_amount
                to_accounts = watched_outputs

        elif out_amount > in_amount:
            # Received more than contributed -> Net INCOMING
            direction = "INCOMING"
            amount = out_amount - in_amount
            to_accounts = watched_outputs
            from_accounts = other_inputs or inputs

        else:
            # In == Out -> Pure 1:1 self-transfer
            direction = "SELF-TRANSFER"
            amount = in_amount
            from_accounts = watched_inputs
            to_accounts = watched_outputs

        # Asset valuation and symbol normalization
        try:
            unit_price = float(sub.get("unit_price_usd", 0) or 0)
        except (TypeError, ValueError):
            unit_price = 0.0
        usd_value = amount * unit_price
        symbol = str(sub.get("symbol", "")).strip().upper() or "N/A"

        # Format and print the match alert to console
        print()
        print(f"[WATCHLIST MATCH] {WATCHED_LABEL} Wallet Activity")
        print(f"   Direction:   {direction}")

        tx_type = str(sub.get("transaction_type", ""))
        if tx_type and tx_type != "transfer":
            print(f"   Type:        {tx_type.upper()}")

        # Print asset amount and optional USD valuation
        if usd_value > 0:
            print(f"   Asset:       {format_amount(amount)} {symbol} (${usd_value:,.2f} USD)")
        else:
            print(f"   Asset:       {format_amount(amount)} {symbol}")

        # Print unit price if available
        if unit_price > 0:
            print(f"   Price:       ${format_amount(unit_price)} USD / {symbol}")

        # Format sender and receiver parties with labels/attributions
        print(f"   From:        {format_parties(from_accounts)}")
        print(f"   To:          {format_parties(to_accounts)}")

        # Print transaction hash, block height, and timestamp
        if transaction.get("hash"):
            print(f"   Tx Hash:     {transaction['hash']}")
        if transaction.get("height"):
            print(f"   Block:       #{transaction['height']}")
        if transaction.get("timestamp"):
            block_time = datetime.fromtimestamp(transaction["timestamp"], tz=timezone.utc)
            print(f"   Timestamp:   {block_time.strftime('%Y-%m-%d %H:%M:%S UTC')}")
        print(DIVIDER)


def main() -> None:
    parser = argparse.ArgumentParser(description="Monitor a blockchain address for incoming and outgoing activity.")
    parser.add_argument(
        "--api-key",
        default="",
        help="Whale Alert API key (defaults to WHALE_ALERT_API_KEY env var)",
    )
    args = parser.parse_args()

    api_key = get_api_key(args.api_key)

    # Listen for interrupt signals for graceful shutdown
    def handle_signal(signum, _frame):
        log.info("Received signal %s. Shutting down gracefully...", signal.Signals(signum).name)
        shutdown.set()

    signal.signal(signal.SIGINT, handle_signal)
    if hasattr(signal, "SIGTERM"):
        signal.signal(signal.SIGTERM, handle_signal)

    # 1. Get the latest block height for the chain
    try:
        latest_height = get_latest_height(BLOCKCHAIN, api_key)
    except (requests.RequestException, RuntimeError, ValueError) as exc:
        log.error("Failed to fetch latest block height for %s: %s", BLOCKCHAIN, exc)
        sys.exit(1)

    log.info(
        "Monitoring %s address %s (%s) from block #%d...",
        BLOCKCHAIN,
        WATCHED_ADDRESS,
        WATCHED_LABEL,
        latest_height,
    )

    # 2. Query initial transactions filtered by the target address starting from the latest block height
    next_url = (
        f"{API_BASE_URL}/{BLOCKCHAIN}/transactions"
        f"?start_height={latest_height}&address={quote(WATCHED_ADDRESS)}"
    )

    # 3. Keep polling periodically using the "next" URL cursor
    while not shutdown.is_set():
        try:
            response = fetch_transactions(next_url, api_key)
        except (requests.RequestException, RuntimeError, ValueError) as exc:
            log.error("Error fetching transactions: %s. Retrying in %ds...", exc, RETRY_DELAY)
            if shutdown.wait(RETRY_DELAY):
                break
            continue

        for transaction in response.get("transactions") or []:
            check_transaction(transaction)

        if response.get("next"):
            next_url = response["next"]

        if shutdown.wait(POLL_INTERVAL):
            break

    log.info("Wallet watcher stopped.")


if __name__ == "__main__":
    main()
