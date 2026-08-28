"""Whale Alert Quickstart: Stablecoin Mints, Burns, Freezes & Locks.

Discovers every blockchain that carries a tracked stablecoin, then polls each one
concurrently for supply and governance events: mints, burns, freezes, unfreezes,
locks, and unlocks above a configurable USD threshold.
"""

import argparse
import logging
import os
import signal
import sys
import threading
from datetime import datetime, timezone
from urllib.parse import urlencode, urlparse, urlunparse, parse_qs

import requests

# Whale Alert API Key.
# Replace this value or set the WHALE_ALERT_API_KEY environment variable.
WHALE_ALERT_API_KEY = "YOUR_API_KEY_HERE"

API_BASE_URL = "https://leviathan.whale-alert.io"
POLL_INTERVAL = 3  # seconds between polls
HTTP_TIMEOUT = 15  # seconds
RETRY_DELAY = 5  # seconds to wait after a failed request

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

# Supply and governance event types this example reports on
TRACKED_EVENT_TYPES = ("mint", "burn", "freeze", "unfreeze", "lock", "unlock")

logging.basicConfig(format="%(asctime)s %(message)s", datefmt="%Y/%m/%d %H:%M:%S", level=logging.INFO)
log = logging.getLogger(__name__)

# Lock for synchronized console output across blockchain workers
print_lock = threading.Lock()

# Set on SIGINT / SIGTERM to stop every worker
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


def fetch_json(session: requests.Session, request_url: str, api_key: str):
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


def fetch_available_blockchains(session: requests.Session, api_key: str) -> list:
    """Retrieve all active blockchains and supported symbols from /status."""
    return fetch_json(session, f"{API_BASE_URL}/status", api_key)


def get_supported_chains(all_chains: list) -> list:
    """Filter blockchains that support at least one tracked stablecoin."""
    return [
        chain
        for chain in all_chains
        if any(str(symbol).lower() in STABLECOINS for symbol in chain.get("symbols") or [])
    ]


def get_latest_height(session: requests.Session, chain: str, api_key: str) -> int:
    """Fetch the newest block height from the status endpoint."""
    status = fetch_json(session, f"{API_BASE_URL}/{chain}/status", api_key)
    return int(status.get("end_height", 0))


def fetch_transactions(session: requests.Session, fetch_url: str, api_key: str) -> dict:
    """Retrieve transactions from the given URL and parse the response.

    The `next` cursor may come back as a relative path, so absolutize it first.
    """
    if not fetch_url.startswith(("http://", "https://")):
        if not fetch_url.startswith("/"):
            fetch_url = "/" + fetch_url
        fetch_url = API_BASE_URL + fetch_url

    return fetch_json(session, fetch_url, api_key)


def sum_field(accounts: list, field: str) -> float:
    """Sum a parsed float field ("amount" or "balance") across a list of accounts."""
    total = 0.0
    for account in accounts:
        try:
            value = float(account.get(field, 0) or 0)
        except (TypeError, ValueError):
            continue
        if value > 0:
            total += value
    return total


def format_asset(amount: float, symbol: str, value_usd: float) -> str:
    """Format token amount, symbol, and USD valuation."""
    symbol_upper = str(symbol).upper()
    if amount <= 0:
        return f"0 {symbol_upper}"

    # Whole numbers print without decimals; fractional amounts keep two.
    amount_str = f"{int(amount):,}" if amount == int(amount) else f"{amount:,.2f}"

    if value_usd > 0:
        return f"{amount_str} {symbol_upper} (${value_usd:,.2f} USD)"
    return f"{amount_str} {symbol_upper}"


def format_blockchain(chain: str) -> str:
    """Format network names into standard title casing."""
    return chain.capitalize() if chain else "Unknown"


def is_null_address(address: str) -> bool:
    """Test whether an address is a standard burn/null address."""
    addr = str(address).strip().lower()
    return (
        addr in ("", "null", "0x0", "t9yd14nj9j7xab4dbgeix9h8unkkhxuwvb")
        or addr.startswith("0x0000000000000000000000000000000000000000")
        or addr.startswith("0x000000000000000000000000000000000000dead")
        or addr.startswith("11111111111111111111111111111111")
    )


def format_short_address(address: str) -> str:
    """Truncate long addresses for clean console output."""
    address = str(address).strip()
    if len(address) > 12:
        return f"{address[:6]}...{address[-4:]}"
    return address


def get_party(accounts: list) -> str:
    """Resolve an account or list of accounts with formatted addresses and owner labels."""
    parties = []
    seen = set()

    for account in accounts:
        address = str(account.get("address", ""))
        owner = str(account.get("owner", ""))
        has_owner = bool(owner) and owner.lower() != "unknown"

        if address and not is_null_address(address):
            if address in seen:
                continue
            seen.add(address)
            if has_owner:
                parties.append(f"{format_short_address(address)} ({owner})")
            else:
                parties.append(format_short_address(address))
        elif has_owner and owner not in seen:
            # Burn/null addresses carry no useful address, so fall back to the entity label.
            seen.add(owner)
            parties.append(owner)

    return ", ".join(parties) if parties else "Unknown"


def check_transaction(chain: str, transaction: dict) -> None:
    """Inspect a transaction's sub-transactions for supply and governance events:
    mints, burns, freezes, unfreezes, locks, and unlocks.
    """
    for sub in transaction.get("sub_transactions") or []:
        symbol = str(sub.get("symbol", ""))
        tx_type = str(sub.get("transaction_type", ""))

        # Step 1: Filter for tracked stablecoins
        if STABLECOINS and symbol.lower() not in STABLECOINS:
            continue

        # Step 2: Filter for supported supply and governance event types
        # (ignores standard transfers and unclassified movements)
        if tx_type not in TRACKED_EVENT_TYPES:
            continue

        inputs = sub.get("inputs") or []
        outputs = sub.get("outputs") or []

        # Step 3: Calculate token amount / address balance based on event type
        if tx_type == "mint":
            # Mint: creates new tokens at the output address(es)
            amount = sum_field(outputs, "amount")
        elif tx_type == "burn":
            # Burn: destroys tokens from the input address(es)
            amount = sum_field(inputs, "amount")
        elif tx_type in ("lock", "unlock"):
            # Lock / Unlock: tokens locked in or released from escrow
            amount = sum_field(inputs, "amount") or sum_field(outputs, "amount")
        elif tx_type == "freeze":
            # Freeze: blacklists input address(es)
            amount = sum_field(inputs, "balance")
        else:
            # Unfreeze: removes blacklist on output address(es)
            amount = sum_field(outputs, "balance")

        # If no positive amount could be calculated for mint/burn/lock/unlock, skip
        if tx_type not in ("freeze", "unfreeze") and amount <= 0:
            continue

        # Calculate USD valuation using the spot price provided by Whale Alert
        try:
            unit_price = float(sub.get("unit_price_usd", 0) or 0)
        except (TypeError, ValueError):
            unit_price = 0.0
        usd_value = amount * unit_price

        # Filter out transactions below the configured minimum USD threshold to avoid noise
        if usd_value < MIN_USD_THRESHOLD:
            continue

        # Step 4: Resolve event-specific parties and labels
        amount_label = "Asset"
        label2 = party2 = ""

        if tx_type == "mint":
            # Mints create new tokens to a recipient/treasury (destination only)
            label1, party1 = "To", get_party(outputs)
        elif tx_type == "burn":
            # Burns destroy tokens from a holder or issuer (source only)
            label1, party1 = "From", get_party(inputs)
        elif tx_type == "freeze":
            # Freezes blacklist a specific target address
            amount_label = "Balance"
            label1, party1 = "Target", get_party(inputs)
        elif tx_type == "unfreeze":
            # Unfreezes restore transfer permissions to a target address
            amount_label = "Balance"
            label1, party1 = "Target", get_party(outputs)
        elif tx_type == "lock":
            # Locks deposit tokens into a smart contract vault or bridge escrow
            label1, party1 = "From", get_party(inputs)
            label2, party2 = "Locked At", get_party(outputs)
        else:
            # Unlocks release escrowed tokens back into active circulation
            label1, party1 = "To", get_party(outputs)

        # Step 5: Format and print event details to console (synchronized)
        with print_lock:
            print()
            print(f"[STABLECOIN {tx_type.upper()}]")
            print(f"   {amount_label + ':':<12} {format_asset(amount, symbol, usd_value)}")
            print(f"   {'Blockchain:':<12} {format_blockchain(chain)}")
            if label1:
                print(f"   {label1 + ':':<12} {party1}")
            if label2:
                print(f"   {label2 + ':':<12} {party2}")
            if transaction.get("hash"):
                print(f"   {'Hash:':<12} {transaction['hash']}")


def monitor_blockchain(chain: dict, api_key: str) -> None:
    """Query and poll a single blockchain for stablecoin events."""
    name = chain.get("name", "")

    # Each worker gets its own session: requests.Session is not designed for
    # concurrent use from multiple threads.
    session = requests.Session()

    start_height = int(chain.get("height", 0) or 0)

    # If height was not in status or is 0, fetch from /{blockchain}/status
    while start_height == 0 and not shutdown.is_set():
        try:
            start_height = get_latest_height(session, name, api_key)
        except (requests.RequestException, RuntimeError, ValueError) as exc:
            log.error("[%s] Failed to fetch latest block height: %s. Retrying in %ds...", name, exc, RETRY_DELAY)
            if shutdown.wait(RETRY_DELAY):
                return

    if shutdown.is_set():
        return

    log.info("[%s] Monitoring from block #%d...", format_blockchain(name), start_height)

    # Query initial transactions from the start block height
    next_url = f"{API_BASE_URL}/{name}/transactions?start_height={start_height}"

    # Keep polling periodically using the "next" URL cursor
    while not shutdown.is_set():
        try:
            response = fetch_transactions(session, next_url, api_key)
        except (requests.RequestException, RuntimeError, ValueError) as exc:
            log.error("[%s] Error fetching transactions: %s. Retrying in %ds...", name, exc, RETRY_DELAY)
            if shutdown.wait(RETRY_DELAY):
                return
            continue

        for transaction in response.get("transactions") or []:
            check_transaction(name, transaction)

        if response.get("next"):
            next_url = response["next"]

        if shutdown.wait(POLL_INTERVAL):
            return


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Monitor stablecoin mints, burns, freezes, unfreezes, locks, and unlocks across all supported chains."
    )
    parser.add_argument(
        "--api-key",
        default="",
        help="Whale Alert API key (defaults to WHALE_ALERT_API_KEY env var)",
    )
    args = parser.parse_args()

    api_key = get_api_key(args.api_key)

    def handle_signal(signum, _frame):
        log.info("Received signal %s. Shutting down gracefully...", signal.Signals(signum).name)
        shutdown.set()

    signal.signal(signal.SIGINT, handle_signal)
    if hasattr(signal, "SIGTERM"):
        signal.signal(signal.SIGTERM, handle_signal)

    now = datetime.now(tz=timezone.utc).strftime("%Y-%m-%d %H:%M:%S")
    print(f"[{now}] Stablecoin Mint, Burn, Freeze & Lock Monitor active (Threshold: ${MIN_USD_THRESHOLD:,.0f} USD)...")

    # 1. Fetch available blockchains and symbols dynamically from the status endpoint
    try:
        all_chains = fetch_available_blockchains(requests.Session(), api_key)
    except (requests.RequestException, RuntimeError, ValueError) as exc:
        log.error("Failed to fetch blockchain status: %s", exc)
        sys.exit(1)

    # 2. Filter blockchains that support our tracked stablecoins
    chains_to_monitor = get_supported_chains(all_chains)
    if not chains_to_monitor:
        log.error("No blockchains found supporting the tracked stablecoins.")
        sys.exit(1)

    chain_names = [chain.get("name", "") for chain in chains_to_monitor]
    log.info(
        "Discovered %d blockchains supporting stablecoins from /status: %s",
        len(chains_to_monitor),
        ", ".join(chain_names),
    )

    # 3. Launch a monitoring thread for each discovered blockchain
    workers = []
    for chain in chains_to_monitor:
        worker = threading.Thread(target=monitor_blockchain, args=(chain, api_key), daemon=True)
        worker.start()
        workers.append(worker)

    # Wait for the shutdown signal. A timed wait keeps the main thread responsive
    # to Ctrl+C on Windows, where an untimed wait() cannot be interrupted.
    try:
        while not shutdown.wait(1):
            pass
    except KeyboardInterrupt:
        shutdown.set()

    log.info("Cancellation signal received. Shutting down gracefully...")

    for worker in workers:
        worker.join()

    log.info("Stablecoin monitor stopped.")


if __name__ == "__main__":
    main()
