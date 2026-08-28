"""Whale Alert Quickstart: WebSocket Live Stream.

Connects to the Whale Alert Live Stream WebSocket API, subscribes to transaction
alerts (or Whale Alert social posts), and prints every incoming event to the console.
Reconnects with backoff and shuts down cleanly on Ctrl+C.
"""

import argparse
import asyncio
import json
import logging
import os
import signal
import sys
from datetime import datetime, timezone

import websockets
from websockets.asyncio.client import connect

# Whale Alert API Key.
# You can replace this value or set the WHALE_ALERT_API_KEY environment variable.
WHALE_ALERT_API_KEY = "YOUR_API_KEY_HERE"

WS_URL = "wss://leviathan.whale-alert.io/ws"
CONNECT_TIMEOUT = 30  # seconds to wait for the opening handshake
PING_INTERVAL = 20  # seconds between client keepalive pings
PING_TIMEOUT = 20  # seconds to wait for a pong before dropping the connection
BACKOFF_SECONDS = 5  # delay between reconnect attempts

# The alert subscription message.
# EDIT THESE VALUES TO CUSTOMIZE YOUR STREAM FILTER
SUBSCRIPTION = {
    # Use "subscribe_alerts" for transaction alerts, or "subscribe_socials" for Whale Alert social media posts.
    "type": "subscribe_alerts",
    #
    # Optional subscription identifier
    # "id": "my_subscription",
    #
    # Filters only valid for "subscribe_alerts" type. Ignored for "subscribe_socials".
    # Blockchains to filter by (lowercase). Leave empty to subscribe to all supported blockchains.
    "blockchains": [
        # "bitcoin",
        # "ethereum",
        # "tron",
    ],
    #
    # Currency symbols to filter by (lowercase). Leave empty to subscribe to all symbols.
    # "symbols": [
    #     "btc", "eth", "usdt", "sol",
    # ],
    #
    # Transaction types to filter by. Leave empty to subscribe to all types.
    # Supported types: "transfer", "mint", "burn", "freeze", "unfreeze", "lock", "unlock"
    "tx_types": [
        # "transfer",
        # "mint",
        # "burn",
        # "freeze",
        # "unfreeze",
        # "lock",
        # "unlock",
    ],
    #
    # Minimum transaction value in USD to trigger an alert
    "min_value_usd": 100_000,
}

logging.basicConfig(format="%(asctime)s %(message)s", datefmt="%Y/%m/%d %H:%M:%S", level=logging.INFO)
log = logging.getLogger(__name__)

SEPARATOR = "=" * 80
DIVIDER = "-" * 80


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


def format_amount(value: float, digits: int = 2) -> str:
    """Format a number with thousands separators and a fixed number of decimals."""
    return f"{value:,.{digits}f}"


def format_timestamp(unix_ts: int) -> str:
    """Render a UNIX timestamp as a UTC datetime string."""
    return datetime.fromtimestamp(unix_ts, tz=timezone.utc).strftime("%Y-%m-%d %H:%M:%S UTC")


def create_alert_text(alert: dict) -> str:
    """Construct a human-readable description for an alert."""
    amounts = ", ".join(
        "{} {} (${} USD)".format(
            format_amount(amount.get("amount", 0.0)),
            str(amount.get("symbol", "")).upper(),
            format_amount(amount.get("value_usd", 0.0), 0),
        )
        for amount in alert.get("amounts") or []
    )

    sender = alert.get("from") or "Unknown"
    receiver = alert.get("to") or "Unknown"
    tx_type = alert.get("transaction_type", "")

    templates = {
        "transfer": f"{amounts} transferred from {sender} to {receiver}",
        "mint": f"{amounts} minted at {receiver}",
        "burn": f"{amounts} burned at {sender}",
        "lock": f"{amounts} locked at {sender}",
        "unlock": f"{amounts} unlocked at {receiver}",
        "freeze": f"{amounts} frozen at {sender}",
        "unfreeze": f"{amounts} unfrozen at {receiver}",
    }
    if tx_type in templates:
        return templates[tx_type]

    return alert.get("text") or f"{tx_type}: {amounts}"


def log_alert(alert: dict) -> None:
    """Output structured alert information to the console."""
    tx_type = alert.get("transaction_type", "")
    sender = alert.get("from", "")
    receiver = alert.get("to", "")

    print()
    print(DIVIDER)
    print(f"[{str(alert.get('blockchain', '')).upper()}] {create_alert_text(alert)}")
    print(f"   Transaction Type: {tx_type}")

    # Mints have no sender and burns have no receiver, so print only the side that exists.
    if tx_type in ("mint", "unlock"):
        if receiver:
            print(f"   To:               {receiver}")
    elif tx_type in ("burn", "lock"):
        if sender:
            print(f"   From:             {sender}")
    elif tx_type == "freeze":
        target = sender or receiver
        if target:
            print(f"   Target:           {target}")
    elif tx_type == "unfreeze":
        target = receiver or sender
        if target:
            print(f"   Target:           {target}")
    else:
        if sender:
            print(f"   From:             {sender}")
        if receiver:
            print(f"   To:               {receiver}")

    for amount in alert.get("amounts") or []:
        print(
            "   Amount:           {} {} (${} USD)".format(
                format_amount(amount.get("amount", 0.0)),
                str(amount.get("symbol", "")).upper(),
                format_amount(amount.get("value_usd", 0.0)),
            )
        )

    transaction = alert.get("transaction") or {}
    if transaction.get("hash"):
        print(f"   Hash:             {transaction['hash']}")
    if transaction.get("height"):
        print(f"   Block Height:     #{transaction['height']}")

    timestamp = alert.get("timestamp", 0)
    print(f"   Timestamp:        {format_timestamp(timestamp)} (UNIX: {timestamp})")
    print(DIVIDER)


def log_socials(socials: dict) -> None:
    """Output a formatted social alert to the console."""
    print()
    print(DIVIDER)
    print(f"[SOCIAL POST] {socials.get('text', '')}")
    if socials.get("blockchain"):
        print(f"   Blockchain: {str(socials['blockchain']).upper()}")
    if socials.get("urls"):
        print(f"   URLs:       {', '.join(socials['urls'])}")
    print(f"   Timestamp:  {format_timestamp(socials.get('timestamp', 0))}")
    print(DIVIDER)


def log_subscription_confirmed(subscription: dict) -> None:
    """Print the filter the server echoed back after a successful subscription."""
    print(SEPARATOR)
    print("SUBSCRIPTION CONFIRMED")
    print(f"  Blockchains: {', '.join(subscription.get('blockchains') or []) or 'all'}")
    print(f"  Symbols:     {', '.join(subscription.get('symbols') or []) or 'all'}")
    print(f"  Tx Types:    {', '.join(subscription.get('tx_types') or []) or 'all'}")
    print(f"  Min USD:     ${format_amount(subscription.get('min_value_usd', 0.0), 0)}")
    print(SEPARATOR)


def build_subscription(subscription: dict) -> str:
    """Serialize the subscription, dropping empty filters.

    An unused filter is left out of the payload entirely rather than sent as an empty list,
    which is what the Go quickstart's `omitempty` tags produce for the same configuration.
    """
    payload = {key: value for key, value in subscription.items() if value or key == "type"}
    return json.dumps(payload)


def handle_message(raw_message: str) -> None:
    """Parse a raw WebSocket frame and dispatch it on its `type` field."""
    try:
        message = json.loads(raw_message)
    except json.JSONDecodeError as exc:
        log.error("Error unmarshalling message: %s", exc)
        return

    message_type = message.get("type", "")

    if message_type == "subscribed_alerts":
        log_subscription_confirmed(message)
    elif message_type == "subscribed_socials":
        print(SEPARATOR)
        print("SUBSCRIPTION CONFIRMED: Subscribed to Whale Alert Socials Stream")
        print(SEPARATOR)
    elif message_type == "alert":
        log_alert(message)
    elif message_type == "socials":
        log_socials(message)
    else:
        log.info("Received message type [%s]: %s", message_type, raw_message)


async def stream(api_key: str) -> None:
    """Connect, subscribe, and read messages until cancelled, reconnecting on failure."""
    endpoint = f"{WS_URL}?api_key={api_key}"

    while True:
        try:
            # The websockets library sends its own keepalive pings and answers server
            # pings automatically, so there is no manual ping loop to maintain here.
            async with connect(
                endpoint,
                open_timeout=CONNECT_TIMEOUT,
                ping_interval=PING_INTERVAL,
                ping_timeout=PING_TIMEOUT,
            ) as socket:
                log.info("Connected to Whale Alert WebSocket server.")

                # Send the alert subscription filter
                await socket.send(build_subscription(SUBSCRIPTION))

                # Keep reading messages until disconnected or stopped
                async for raw_message in socket:
                    handle_message(raw_message)

            log.info("Connection closed by server. Reconnecting in %ds...", BACKOFF_SECONDS)
        except asyncio.CancelledError:
            raise
        except websockets.exceptions.ConnectionClosed as exc:
            log.error("Connection error: %s. Reconnecting in %ds...", exc, BACKOFF_SECONDS)
        except OSError as exc:
            log.error("Error connecting to WebSocket: %s. Retrying in %ds...", exc, BACKOFF_SECONDS)
        except websockets.exceptions.WebSocketException as exc:
            log.error("WebSocket error: %s. Retrying in %ds...", exc, BACKOFF_SECONDS)

        await asyncio.sleep(BACKOFF_SECONDS)


async def main_async(api_key: str) -> None:
    """Run the stream until an interrupt or termination signal arrives."""
    task = asyncio.create_task(stream(api_key))

    # SIGTERM is not delivered as KeyboardInterrupt, so cancel the stream explicitly.
    # add_signal_handler is Unix-only; on Windows Ctrl+C still raises KeyboardInterrupt.
    loop = asyncio.get_running_loop()
    for signal_name in ("SIGTERM", "SIGINT"):
        sig = getattr(signal, signal_name, None)
        if sig is None:
            continue
        try:
            loop.add_signal_handler(sig, task.cancel)
        except NotImplementedError:
            pass

    log.info("Whale Alert streaming client started. Press Ctrl+C to exit.")

    try:
        await task
    except asyncio.CancelledError:
        pass


def main() -> None:
    parser = argparse.ArgumentParser(description="Stream live Whale Alert transaction alerts over WebSocket.")
    parser.add_argument(
        "--api-key",
        default="",
        help="Whale Alert API key (defaults to WHALE_ALERT_API_KEY env var)",
    )
    args = parser.parse_args()

    api_key = get_api_key(args.api_key)

    try:
        asyncio.run(main_async(api_key))
    except KeyboardInterrupt:
        log.info("Received interrupt. Shutting down gracefully...")

    log.info("WebSocket client stopped.")


if __name__ == "__main__":
    main()
