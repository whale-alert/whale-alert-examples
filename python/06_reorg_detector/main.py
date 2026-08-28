"""Whale Alert Quickstart: Chain Reorganization & Orphan Block Detector.

Tracks the canonical chain tip, keeps a sliding window of recent block hashes, and
re-verifies the previous block on every new arrival. When a hash changes, it walks
backwards to the common ancestor and reports the reorg depth, the orphaned blocks,
and the canonical blocks that replaced them.
"""

import argparse
import logging
import os
import re
import signal
import sys
import threading
import time
from datetime import datetime, timezone
from urllib.parse import urlencode, urlparse, urlunparse, parse_qs

import requests

# Whale Alert API Key.
# Replace this value or set the WHALE_ALERT_API_KEY environment variable.
WHALE_ALERT_API_KEY = "YOUR_API_KEY_HERE"

API_BASE_URL = "https://leviathan.whale-alert.io"
DEFAULT_BLOCKCHAIN = "ethereum"
DEFAULT_HISTORY_SIZE = 20
MIN_HISTORY_SIZE = 5
DEFAULT_POLL_INTERVAL = 3.0  # seconds
MIN_POLL_INTERVAL = 0.5  # seconds

HTTP_TIMEOUT = 15  # seconds
MAX_RETRIES = 3
INITIAL_BACKOFF = 1  # seconds

logging.basicConfig(format="%(asctime)s %(message)s", datefmt="%Y/%m/%d %H:%M:%S", level=logging.INFO)
log = logging.getLogger(__name__)

# The reorg banner contains a non-ASCII warning sign. Windows uses the legacy code page
# for a redirected stdout, so force UTF-8 to keep `python main.py > log.txt` from failing.
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

session = requests.Session()

# Set on SIGINT / SIGTERM to stop the monitoring loop
shutdown = threading.Event()

REPORT_WIDTH = 83

DURATION_PATTERN = re.compile(r"(\d+(?:\.\d+)?)(ms|s|m|h)")
DURATION_UNIT_SECONDS = {"ms": 0.001, "s": 1.0, "m": 60.0, "h": 3600.0}


class BlockNotFoundError(Exception):
    """Raised when a requested block has not been mined yet (HTTP 404)."""


class CachedBlock:
    """Essential block metadata held in the local sliding window history."""

    __slots__ = ("height", "hash", "timestamp", "tx_count", "seen_at")

    def __init__(self, height: int, block_hash: str, timestamp: int, tx_count: int):
        self.height = height
        self.hash = block_hash
        self.timestamp = timestamp
        self.tx_count = tx_count
        self.seen_at = time.time()


class BlockCache:
    """A sliding window of recent block hashes, keyed by height.

    Only the monitoring loop touches this, so no locking is needed.
    """

    def __init__(self, max_size: int):
        if max_size < MIN_HISTORY_SIZE:
            # Callers validate --history; this guards against a programming error.
            raise ValueError(f"BlockCache: max_size {max_size} below minimum {MIN_HISTORY_SIZE}")
        self.max_size = max_size
        self.order: list = []
        self.by_height: dict = {}

    def put(self, block: CachedBlock) -> None:
        """Add or update a block and evict entries past max_size."""
        if block.height not in self.by_height:
            self.order.append(block.height)
        self.by_height[block.height] = block

        while len(self.order) > self.max_size:
            del self.by_height[self.order.pop(0)]

    def get(self, height: int):
        """Return the cached block at a given height, or None."""
        return self.by_height.get(height)

    def __len__(self) -> int:
        return len(self.order)

    def oldest_height(self):
        """Return the lowest cached block height, or None when empty."""
        return self.order[0] if self.order else None

    def latest_height(self):
        """Return the highest cached block height, or None when empty."""
        return self.order[-1] if self.order else None

    def latest(self):
        """Return the most recently added block, or None when empty."""
        return self.by_height[self.order[-1]] if self.order else None


class TrackerStats:
    """Operational metrics collected during execution."""

    def __init__(self):
        self.start_time = time.time()
        self.blocks_scanned = 0
        self.reorg_events_count = 0
        self.total_orphan_blocks = 0
        self.max_reorg_depth = 0


def resolve_api_key(flag_key: str) -> str:
    """Inspect CLI flags, environment variables, and fallback constants."""
    if flag_key:
        return flag_key
    return os.environ.get("WHALE_ALERT_API_KEY", "") or WHALE_ALERT_API_KEY


def parse_duration(value: str) -> float:
    """Parse a Go-style duration ('3s', '500ms', '2m30s') or a bare number of seconds."""
    value = value.strip().lower()
    if not value:
        raise ValueError("duration cannot be empty")

    try:
        return float(value)
    except ValueError:
        pass

    parts = DURATION_PATTERN.findall(value)
    if not parts or "".join(amount + unit for amount, unit in parts) != value:
        raise ValueError(f"unrecognized duration {value!r} (expected e.g. '3s', '500ms', '2m30s')")

    return sum(float(amount) * DURATION_UNIT_SECONDS[unit] for amount, unit in parts)


def format_duration(seconds: float) -> str:
    """Render a number of seconds the way Go prints a Duration ('500ms', '12s', '1m24s')."""
    sign = "-" if seconds < 0 else ""
    seconds = abs(seconds)

    # Sub-second values render as milliseconds so a --poll of 500ms reads back as "500ms".
    if seconds < 1:
        return f"{sign}{seconds * 1000:.0f}ms"

    total = int(round(seconds))
    if total < 60:
        return f"{sign}{total}s"

    minutes, secs = divmod(total, 60)
    if minutes < 60:
        return f"{sign}{minutes}m{secs}s"

    hours, minutes = divmod(minutes, 60)
    return f"{sign}{hours}h{minutes}m{secs}s"


def format_short_hash(block_hash: str) -> str:
    """Format full 64-character hashes for clean console presentation."""
    if len(block_hash) > 18:
        return f"{block_hash[:10]}...{block_hash[-8:]}"
    return block_hash


def fetch_json(request_url: str, api_key: str):
    """Execute an HTTP GET request with retries, mapping 404 to BlockNotFoundError."""
    if not request_url.startswith("http"):
        request_url = API_BASE_URL + request_url

    parsed = urlparse(request_url)
    if api_key and api_key != "YOUR_API_KEY_HERE" and "api_key" not in parse_qs(parsed.query):
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

            if response.status_code == 404:
                raise BlockNotFoundError("resource not found")

            body = response.text.strip()
            status = f"HTTP {response.status_code} ({response.reason})"
            last_error = RuntimeError(f"{status}: {body}" if body else status)

            # Fatal client errors (400, 401, 403) are not retryable
            if response.status_code != 429 and response.status_code < 500:
                raise last_error

            # Only announce a retry when one is actually coming.
            if attempt < MAX_RETRIES:
                if response.status_code == 429:
                    log.info("Rate limit (HTTP 429). Retrying attempt %d/%d in %ds...",
                             attempt, MAX_RETRIES, backoff)
                else:
                    log.info("Server error (HTTP %d). Retrying attempt %d/%d in %ds...",
                             response.status_code, attempt, MAX_RETRIES, backoff)

        # Only back off between attempts. Sleeping after the final attempt just
        # delays the error the caller is already going to get.
        if attempt < MAX_RETRIES:
            if shutdown.wait(backoff):
                raise RuntimeError("cancelled")
            backoff *= 2

    raise RuntimeError(f"request failed after {MAX_RETRIES} retries: {last_error}")


def fetch_block(chain: str, height: int, api_key: str) -> dict:
    """Retrieve block details by height from GET /{blockchain}/block/{height}."""
    return fetch_json(f"{API_BASE_URL}/{chain}/block/{height}", api_key)


def get_blockchain_status(chain: str, api_key: str) -> dict:
    """Retrieve current network height from GET /{blockchain}/status."""
    return fetch_json(f"{API_BASE_URL}/{chain}/status", api_key)


def to_cached(block: dict) -> CachedBlock:
    """Convert an API block payload into a cache entry."""
    return CachedBlock(
        height=int(block.get("height", 0)),
        block_hash=str(block.get("hash", "")),
        timestamp=int(block.get("timestamp", 0)),
        tx_count=len(block.get("transactions") or []),
    )


def handle_reorganization(chain: str, api_key: str, current_tip: int, cache: BlockCache,
                          canonical_tip: dict) -> dict:
    """Walk backward from current_tip to find the common ancestor and describe the reorg."""
    oldest_cached = cache.oldest_height() or 0

    orphaned = []
    canonical = []

    # Add the reorged tip block
    old_tip = cache.get(current_tip)
    if old_tip is not None:
        orphaned.append(old_tip)
    canonical.append(to_cached(canonical_tip))

    common_ancestor_height = 0
    common_ancestor_hash = ""
    found_ancestor = False

    # Walk backwards block-by-block comparing the canonical network block vs the cached block
    height = current_tip - 1
    while height >= oldest_cached and height > 0:
        if shutdown.is_set():
            raise RuntimeError("cancelled")

        cached_block = cache.get(height)
        if cached_block is None:
            break

        network_block = fetch_block(chain, height, api_key)

        if cached_block.hash.lower() == str(network_block.get("hash", "")).lower():
            # Found Common Ancestor!
            common_ancestor_height = height
            common_ancestor_hash = cached_block.hash
            found_ancestor = True
            break

        # This block was also orphaned as part of a multi-block reorg
        orphaned.insert(0, cached_block)
        canonical.insert(0, to_cached(network_block))

        height -= 1

    if not found_ancestor:
        common_ancestor_height = oldest_cached - 1
        common_ancestor_hash = "unknown (exceeded local history window)"

    # Update local cache: replace orphaned blocks with canonical blocks
    for block in canonical:
        cache.put(block)

    return {
        "blockchain": chain,
        "detected_at": datetime.now(tz=timezone.utc),
        "depth": len(orphaned),
        "fork_height": common_ancestor_height,
        "fork_hash": common_ancestor_hash,
        "orphaned_blocks": orphaned,
        "canonical_blocks": canonical,
    }


def print_header(chain: str, start_height: int, history_size: int, poll_interval: float) -> None:
    """Display the startup dashboard banner."""
    print()
    print("=" * REPORT_WIDTH)
    print(f"           WHALE ALERT CHAIN REORGANIZATION & ORPHAN DETECTOR ({chain.upper()})")
    print("=" * REPORT_WIDTH)
    print(f"Blockchain Network  : {chain.upper()}")
    print(f"Starting Height     : #{start_height:,}")
    print(f"Sliding Cache Window: {history_size} blocks")
    print(f"Polling Interval    : {format_duration(poll_interval)}")
    print("=" * REPORT_WIDTH)


def print_block_tick(block: CachedBlock, stats: TrackerStats) -> None:
    """Log a single canonical block confirmation."""
    age = time.time() - block.timestamp
    now = datetime.now(tz=timezone.utc).strftime("%H:%M:%S")
    height = f"{block.height:,}"

    print(
        f"[{now}] Block #{height:<10} | Hash: {format_short_hash(block.hash)} | "
        f"{block.tx_count:3d} txs | Mined: {format_duration(age)} ago | "
        f"Reorgs: {stats.reorg_events_count}"
    )


def print_reorg_alert(event: dict) -> None:
    """Print an eye-catching alert detailing a detected chain reorg."""
    print()
    print("!" * REPORT_WIDTH)
    print("⚠️  ALERT: BLOCKCHAIN REORGANIZATION (REORG / ORPHAN) DETECTED!")
    print("!" * REPORT_WIDTH)
    print(f"Detected At         : {event['detected_at'].strftime('%Y-%m-%d %H:%M:%S.%f')[:-3]} UTC")
    print(f"Blockchain Network  : {event['blockchain'].upper()}")
    print(f"Reorganization Depth: {event['depth']} BLOCK(S)")
    print(f"Common Ancestor     : Block #{event['fork_height']:,} "
          f"(Hash: {format_short_hash(event['fork_hash'])})")
    print("-" * REPORT_WIDTH)
    print("DISCARDED / ORPHANED BLOCKS (Old Branch):")
    for index, block in enumerate(event["orphaned_blocks"], start=1):
        print(f"  [{index}] Height #{block.height:<10,} | Orphan Hash   : {block.hash} ({block.tx_count} txs)")
    print("-" * REPORT_WIDTH)
    print("NEW CANONICAL REPLACEMENT BLOCKS (New Branch):")
    for index, block in enumerate(event["canonical_blocks"], start=1):
        print(f"  [{index}] Height #{block.height:<10,} | Canonical Hash: {block.hash} ({block.tx_count} txs)")
    print("=" * REPORT_WIDTH)
    print()


def print_summary_report(chain: str, stats: TrackerStats, cache: BlockCache) -> None:
    """Print overall operational statistics upon shutdown."""
    print()
    print("=" * REPORT_WIDTH)
    print(f"                   REORG DETECTOR SUMMARY REPORT ({chain.upper()})")
    print("=" * REPORT_WIDTH)
    print(f"Total Elapsed Time  : {format_duration(time.time() - stats.start_time)}")
    print(f"Blocks Monitored    : {stats.blocks_scanned:,}")
    print(f"Reorg Events Count  : {stats.reorg_events_count}")
    print(f"Total Orphan Blocks : {stats.total_orphan_blocks}")
    print(f"Max Reorg Depth     : {stats.max_reorg_depth} block(s)")
    oldest = cache.oldest_height()
    if oldest is not None:
        print(f"Active Cache Window : #{oldest:,} to #{cache.latest_height():,} ({len(cache)} blocks)")
    print("=" * REPORT_WIDTH)


def main() -> None:
    # 1. Parse command-line flags
    parser = argparse.ArgumentParser(
        description="Detect blockchain reorganizations, orphan blocks, and uncle blocks in real time."
    )
    parser.add_argument("--blockchain", default=DEFAULT_BLOCKCHAIN,
                        help="Target blockchain network (e.g. ethereum, bitcoin, tron, dogecoin)")
    parser.add_argument("--history", type=int, default=DEFAULT_HISTORY_SIZE,
                        help="Number of recent block hashes to retain in sliding window cache")
    parser.add_argument("--poll", default="3s",
                        help="Polling interval when awaiting new blocks (e.g. 3s, 500ms, 1m)")
    parser.add_argument("--start", type=int, default=0,
                        help="Starting block height (0 for latest network block)")
    parser.add_argument("--api-key", default="",
                        help="Whale Alert API key (defaults to WHALE_ALERT_API_KEY env var)")
    args = parser.parse_args()

    api_key = resolve_api_key(args.api_key)
    if not api_key or api_key == "YOUR_API_KEY_HERE":
        log.error(
            "WHALE_ALERT_API_KEY is not set. Block queries require an active API key. "
            "Set the environment variable, pass --api-key, or edit the constant in main.py."
        )
        sys.exit(1)

    chain = args.blockchain.strip().lower()

    history_size = args.history
    if history_size < MIN_HISTORY_SIZE:
        log.error("Invalid --history %d: the sliding window must retain at least %d blocks.",
                  history_size, MIN_HISTORY_SIZE)
        sys.exit(1)

    try:
        poll_interval = parse_duration(args.poll)
    except ValueError as exc:
        log.error("Invalid --poll %r: %s", args.poll, exc)
        sys.exit(1)
    poll_interval = max(poll_interval, MIN_POLL_INTERVAL)

    # Listen for Ctrl+C or termination signals for clean, graceful exit
    def handle_signal(signum, _frame):
        log.info("Received signal %s. Shutting down gracefully...", signal.Signals(signum).name)
        shutdown.set()

    signal.signal(signal.SIGINT, handle_signal)
    if hasattr(signal, "SIGTERM"):
        signal.signal(signal.SIGTERM, handle_signal)

    stats = TrackerStats()
    cache = BlockCache(history_size)

    # 2. Resolve starting block height
    start_height = args.start
    if start_height == 0:
        try:
            status = get_blockchain_status(chain, api_key)
        except (requests.RequestException, BlockNotFoundError, RuntimeError, ValueError) as exc:
            log.error("Failed to fetch blockchain status for %s: %s", chain, exc)
            sys.exit(1)
        start_height = int(status.get("end_height", 0))

    if start_height == 0:
        log.error("Cannot monitor %s: resolved chain tip is block 0 (no blocks available).", chain)
        sys.exit(1)

    print_header(chain, start_height, history_size, poll_interval)

    # 3. Pre-seed cache with initial blocks up to start_height
    # Seed the full window so deep reorgs are detectable immediately at startup
    # rather than only after history_size blocks have elapsed. Costs one API call per block.
    pre_seed_count = min(history_size, start_height)
    seed_start = start_height - pre_seed_count + 1

    log.info("Pre-seeding cache with %d initial blocks (#%s to #%s)...",
             pre_seed_count, f"{seed_start:,}", f"{start_height:,}")

    for height in range(seed_start, start_height + 1):
        if shutdown.is_set():
            return
        try:
            block = fetch_block(chain, height, api_key)
        except (requests.RequestException, BlockNotFoundError, RuntimeError, ValueError) as exc:
            log.warning("Warning: skipping block #%d during seeding: %s", height, exc)
            continue
        cache.put(to_cached(block))

    tip = cache.latest()
    if tip is None:
        log.error("Seeding produced no blocks; cannot establish a baseline tip for %s.", chain)
        sys.exit(1)

    log.info("Cache initialized with %d blocks. Latest tip: #%s (%s)",
             len(cache), f"{tip.height:,}", format_short_hash(tip.hash))
    print("-" * REPORT_WIDTH)
    log.info("Starting real-time block watcher and reorg detection loop...")
    print("-" * REPORT_WIDTH)

    current_tip = start_height

    # 4. Main monitoring and reorg detection loop
    while not shutdown.is_set():
        next_height = current_tip + 1

        # Attempt to fetch the next block
        try:
            next_block = fetch_block(chain, next_height, api_key)
        except BlockNotFoundError:
            # Block not mined yet, wait and poll again
            if shutdown.wait(poll_interval):
                break
            continue
        except (requests.RequestException, RuntimeError, ValueError) as exc:
            if shutdown.is_set():
                break
            log.error("Error fetching block #%s: %s. Retrying in %s...",
                      f"{next_height:,}", exc, format_duration(poll_interval))
            if shutdown.wait(poll_interval):
                break
            continue

        # 5. Verify integrity of the previous block (height - 1) against our local cache
        cached_prev = cache.get(current_tip)
        if cached_prev is not None:
            # Fetch the canonical version of current_tip from the network to see if its hash changed
            canonical_prev = None
            try:
                canonical_prev = fetch_block(chain, current_tip, api_key)
            except BlockNotFoundError:
                pass
            except (requests.RequestException, RuntimeError, ValueError) as exc:
                log.warning("Warning: Failed to verify previous block #%s: %s", f"{current_tip:,}", exc)

            # Compare stored hash vs current canonical network hash
            if canonical_prev is not None and cached_prev.hash.lower() != str(canonical_prev.get("hash", "")).lower():
                # REORGANIZATION DETECTED!
                try:
                    event = handle_reorganization(chain, api_key, current_tip, cache, canonical_prev)
                except (requests.RequestException, BlockNotFoundError, RuntimeError, ValueError) as exc:
                    log.error("Error during reorg ancestor traversal: %s", exc)
                else:
                    stats.reorg_events_count += 1
                    stats.total_orphan_blocks += event["depth"]
                    stats.max_reorg_depth = max(stats.max_reorg_depth, event["depth"])
                    print_reorg_alert(event)

        # Store next block in cache and advance tip
        next_cached = to_cached(next_block)
        cache.put(next_cached)
        current_tip = next_height
        stats.blocks_scanned += 1

        print_block_tick(next_cached, stats)

    # 6. Print closing summary upon graceful termination
    print_summary_report(chain, stats, cache)


if __name__ == "__main__":
    main()
