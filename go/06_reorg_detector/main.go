package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
)

const (
	// Whale Alert API Key.
	// Replace this value or set the WHALE_ALERT_API_KEY environment variable.
	whaleAlertApiKey = "YOUR_API_KEY_HERE"

	defaultBlockchain   = "ethereum"
	defaultHistorySize  = 20
	minHistorySize      = 5
	defaultPollInterval = 3 * time.Second

	httpTimeout    = 15 * time.Second
	maxRetries     = 3
	initialBackoff = 1 * time.Second
)

var (
	apiBaseURL = "https://leviathan.whale-alert.io"
)

// ErrNotFound is returned when a requested block has not been mined yet (HTTP 404).
var ErrNotFound = errors.New("resource not found")

// HTTP client with configured timeout.
var httpClient = &http.Client{
	Timeout: httpTimeout,
}

// BlockchainStatus represents the response from GET /{blockchain}/status.
type BlockchainStatus struct {
	StartHeight   uint64 `json:"start_height"`
	EndHeight     uint64 `json:"end_height"`
	MinPlanHeight uint64 `json:"min_plan_height"`
}

// BlockResponse represents the response from GET /{blockchain}/block/{height}.
type BlockResponse struct {
	Height       uint64        `json:"height"`
	Hash         string        `json:"hash"`
	Timestamp    int64         `json:"timestamp"`
	Transactions []Transaction `json:"transactions,omitempty"`
}

// Transaction represents a transaction inside a block.
type Transaction struct {
	Height          uint64           `json:"height"`
	IndexInBlock    uint64           `json:"index_in_block"`
	Timestamp       int64            `json:"timestamp"`
	Hash            string           `json:"hash"`
	Fee             string           `json:"fee,omitempty"`
	FeeSymbol       string           `json:"fee_symbol,omitempty"`
	FeeSymbolPrice  float64          `json:"fee_symbol_price,omitempty"`
	SubTransactions []SubTransaction `json:"sub_transactions,omitempty"`
}

// SubTransaction represents an asset movement within a transaction.
type SubTransaction struct {
	Symbol          string    `json:"symbol"`
	TransactionType string    `json:"transaction_type"`
	UnitPriceUSD    float64   `json:"unit_price_usd"`
	Inputs          []Account `json:"inputs"`
	Outputs         []Account `json:"outputs"`
}

// Account represents a sender or receiver in a sub-transaction.
type Account struct {
	Address     string `json:"address"`
	Amount      string `json:"amount"`
	Balance     string `json:"balance,omitempty"`
	Locked      string `json:"locked,omitempty"`
	IsFrozen    bool   `json:"is_frozen,omitempty"`
	Owner       string `json:"owner,omitempty"`
	OwnerType   string `json:"owner_type,omitempty"`
	AddressType string `json:"address_type,omitempty"`
}

// CachedBlock stores essential block metadata in the local sliding window history.
type CachedBlock struct {
	Height    uint64
	Hash      string
	Timestamp int64
	TxCount   int
	SeenAt    time.Time
}

// ReorgEvent captures all details of a detected blockchain reorganization.
type ReorgEvent struct {
	Blockchain      string
	DetectedAt      time.Time
	Depth           int
	ForkHeight      uint64
	ForkHash        string
	OrphanedBlocks  []CachedBlock
	CanonicalBlocks []CachedBlock
}

// BlockCache implements a thread-safe sliding window of recent block hashes.
type BlockCache struct {
	mu       sync.RWMutex
	maxSize  int
	order    []uint64
	byHeight map[uint64]CachedBlock
}

// NewBlockCache creates a cache retaining up to maxSize recent blocks.
func NewBlockCache(maxSize int) *BlockCache {
	if maxSize < minHistorySize {
		// Callers validate -history; this guards against a programming error.
		panic(fmt.Sprintf("NewBlockCache: maxSize %d below minimum %d", maxSize, minHistorySize))
	}
	return &BlockCache{
		maxSize:  maxSize,
		order:    make([]uint64, 0, maxSize),
		byHeight: make(map[uint64]CachedBlock),
	}
}

// Put adds or updates a block in the cache and evicts old entries past maxSize.
func (c *BlockCache) Put(b CachedBlock) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.byHeight[b.Height]; !exists {
		c.order = append(c.order, b.Height)
	}
	c.byHeight[b.Height] = b

	for len(c.order) > c.maxSize {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.byHeight, oldest)
	}
}

// Get returns the cached block for a given height.
func (c *BlockCache) Get(height uint64) (CachedBlock, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.byHeight[height]
	return b, ok
}

// Len returns the current number of cached blocks.
func (c *BlockCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.order)
}

// OldestHeight returns the lowest block height currently cached.
func (c *BlockCache) OldestHeight() (uint64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.order) == 0 {
		return 0, false
	}
	return c.order[0], true
}

// LatestHeight returns the highest block height currently cached.
func (c *BlockCache) LatestHeight() (uint64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.order) == 0 {
		return 0, false
	}
	return c.order[len(c.order)-1], true
}

// Latest returns the most recently added block in the cache.
func (c *BlockCache) Latest() (CachedBlock, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.order) == 0 {
		return CachedBlock{}, false
	}
	return c.byHeight[c.order[len(c.order)-1]], true
}

// List returns all cached blocks in ascending height order.
func (c *BlockCache) List() []CachedBlock {
	c.mu.RLock()
	defer c.mu.RUnlock()
	blocks := make([]CachedBlock, 0, len(c.order))
	for _, h := range c.order {
		blocks = append(blocks, c.byHeight[h])
	}
	return blocks
}

// TrackerStats tracks operational metrics during execution.
type TrackerStats struct {
	StartTime         time.Time
	BlocksScanned     uint64
	ReorgEventsCount  uint64
	TotalOrphanBlocks uint64
	MaxReorgDepth     int
}

func main() {
	// 1. Parse command-line flags
	blockchainFlag := flag.String("blockchain", defaultBlockchain, "Target blockchain network (e.g. ethereum, bitcoin, tron, dogecoin)")
	historySizeFlag := flag.Int("history", defaultHistorySize, "Number of recent block hashes to retain in sliding window cache")
	pollIntervalFlag := flag.Duration("poll", defaultPollInterval, "Polling interval when awaiting new blocks")
	startBlockFlag := flag.Uint64("start", 0, "Starting block height (0 for latest network block)")
	apiKeyFlag := flag.String("api-key", "", "Whale Alert API key (defaults to WHALE_ALERT_API_KEY env var)")

	flag.Parse()

	apiKey := resolveAPIKey(*apiKeyFlag)
	if apiKey == "" || apiKey == "YOUR_API_KEY_HERE" {
		log.Fatal("WHALE_ALERT_API_KEY is not set. Block queries require an active API key. Set the environment variable, pass -api-key, or edit the constant in main.go.")
	}

	chain := strings.ToLower(strings.TrimSpace(*blockchainFlag))
	historySize := *historySizeFlag
	if historySize < minHistorySize {
		log.Fatalf("Invalid -history %d: the sliding window must retain at least %d blocks.", historySize, minHistorySize)
	}
	pollInterval := *pollIntervalFlag
	if pollInterval < 500*time.Millisecond {
		pollInterval = 500 * time.Millisecond
	}

	// Listen for Ctrl+C or termination signals for clean, graceful exit
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stats := &TrackerStats{
		StartTime: time.Now(),
	}

	cache := NewBlockCache(historySize)

	// 2. Resolve starting block height
	startHeight := *startBlockFlag
	if startHeight == 0 {
		status, err := getBlockchainStatus(ctx, chain, apiKey)
		if err != nil {
			log.Fatalf("Failed to fetch blockchain status for %s: %v", chain, err)
		}
		startHeight = status.EndHeight
	}
	if startHeight == 0 {
		log.Fatalf("Cannot monitor %s: resolved chain tip is block 0 (no blocks available).", chain)
	}

	printHeader(chain, startHeight, historySize, pollInterval)

	// 3. Pre-seed cache with initial blocks up to startHeight
	// Seed the full window so deep reorgs are detectable immediately at startup
	// rather than only after historySize blocks have elapsed. Costs one API call
	// per block. Stays in uint64 so a short chain cannot underflow.
	preSeedCount := uint64(historySize)
	if preSeedCount > startHeight {
		preSeedCount = startHeight
	}

	seedStart := startHeight - preSeedCount + 1
	log.Printf("Pre-seeding cache with %d initial blocks (#%s to #%s)...\n",
		preSeedCount,
		humanize.Comma(int64(seedStart)),
		humanize.Comma(int64(startHeight)),
	)

	for h := seedStart; h <= startHeight; h++ {
		if ctx.Err() != nil {
			return
		}
		blk, err := fetchBlock(ctx, chain, h, apiKey)
		if err != nil {
			log.Printf("Warning: skipping block #%d during seeding: %v", h, err)
			continue
		}
		cache.Put(CachedBlock{
			Height:    blk.Height,
			Hash:      blk.Hash,
			Timestamp: blk.Timestamp,
			TxCount:   len(blk.Transactions),
			SeenAt:    time.Now(),
		})
	}

	tip, ok := cache.Latest()
	if !ok {
		log.Fatalf("Seeding produced no blocks; cannot establish a baseline tip for %s.", chain)
	}
	log.Printf("Cache initialized with %d blocks. Latest tip: #%s (%s)\n",
		cache.Len(),
		humanize.Comma(int64(tip.Height)),
		formatShortHash(tip.Hash),
	)
	fmt.Println("-----------------------------------------------------------------------------------")
	log.Println("Starting real-time block watcher and reorg detection loop...")
	fmt.Println("-----------------------------------------------------------------------------------")

	currentTip := startHeight

	// 4. Main monitoring and reorg detection loop
	for ctx.Err() == nil {
		nextHeight := currentTip + 1

		// Attempt to fetch the next block
		nextBlock, err := fetchBlock(ctx, chain, nextHeight, apiKey)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				// Block not mined yet, wait and poll again
				if !sleepContext(ctx, pollInterval) {
					break
				}
				continue
			}

			if ctx.Err() != nil {
				break
			}

			log.Printf("Error fetching block #%s: %v. Retrying in %v...", humanize.Comma(int64(nextHeight)), err, pollInterval)
			if !sleepContext(ctx, pollInterval) {
				break
			}
			continue
		}

		// 5. Verify integrity of the previous block (height - 1) against our local cache
		cachedPrev, hasPrev := cache.Get(currentTip)
		if hasPrev {
			// Fetch the canonical version of currentTip from the network to verify if its hash changed
			canonicalPrev, err := fetchBlock(ctx, chain, currentTip, apiKey)
			if err != nil && !errors.Is(err, ErrNotFound) {
				log.Printf("Warning: Failed to verify previous block #%s: %v", humanize.Comma(int64(currentTip)), err)
			} else if canonicalPrev != nil {
				// Compare stored hash vs current canonical network hash
				if !strings.EqualFold(cachedPrev.Hash, canonicalPrev.Hash) {
					// REORGANIZATION DETECTED!
					reorgEvent, rErr := handleReorganization(ctx, chain, apiKey, currentTip, cache, canonicalPrev)
					if rErr != nil {
						log.Printf("Error during reorg ancestor traversal: %v", rErr)
					} else if reorgEvent != nil {
						stats.ReorgEventsCount++
						stats.TotalOrphanBlocks += uint64(reorgEvent.Depth)
						if reorgEvent.Depth > stats.MaxReorgDepth {
							stats.MaxReorgDepth = reorgEvent.Depth
						}
						printReorgAlert(reorgEvent)
					}
				}
			}
		}

		// Store next block in cache and advance tip
		nextCached := CachedBlock{
			Height:    nextBlock.Height,
			Hash:      nextBlock.Hash,
			Timestamp: nextBlock.Timestamp,
			TxCount:   len(nextBlock.Transactions),
			SeenAt:    time.Now(),
		}
		cache.Put(nextCached)
		currentTip = nextHeight
		stats.BlocksScanned++

		printBlockTick(nextCached, stats)
	}

	// 6. Print closing summary upon graceful termination
	printSummaryReport(chain, stats, cache)
}

// handleReorganization walks backward from currentTip to find the common ancestor and reports the reorg.
func handleReorganization(
	ctx context.Context,
	chain, apiKey string,
	currentTip uint64,
	cache *BlockCache,
	canonicalTip *BlockResponse,
) (*ReorgEvent, error) {
	oldestCached, _ := cache.OldestHeight()

	orphaned := make([]CachedBlock, 0)
	canonical := make([]CachedBlock, 0)

	// Add the reorged tip block
	if oldTip, ok := cache.Get(currentTip); ok {
		orphaned = append(orphaned, oldTip)
	}
	canonical = append(canonical, CachedBlock{
		Height:    canonicalTip.Height,
		Hash:      canonicalTip.Hash,
		Timestamp: canonicalTip.Timestamp,
		TxCount:   len(canonicalTip.Transactions),
		SeenAt:    time.Now(),
	})

	var commonAncestorHeight uint64
	var commonAncestorHash string
	foundAncestor := false

	// Walk backwards block-by-block comparing canonical network block vs cached block
	for h := currentTip - 1; h >= oldestCached && h > 0; h-- {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		cachedBlk, ok := cache.Get(h)
		if !ok {
			break
		}

		netBlk, err := fetchBlock(ctx, chain, h, apiKey)
		if err != nil {
			return nil, fmt.Errorf("failed fetching block #%d during ancestor search: %w", h, err)
		}

		if strings.EqualFold(cachedBlk.Hash, netBlk.Hash) {
			// Found Common Ancestor!
			commonAncestorHeight = h
			commonAncestorHash = cachedBlk.Hash
			foundAncestor = true
			break
		}

		// This block was also orphaned as part of a multi-block reorg
		orphaned = append([]CachedBlock{cachedBlk}, orphaned...)
		canonical = append([]CachedBlock{{
			Height:    netBlk.Height,
			Hash:      netBlk.Hash,
			Timestamp: netBlk.Timestamp,
			TxCount:   len(netBlk.Transactions),
			SeenAt:    time.Now(),
		}}, canonical...)
	}

	if !foundAncestor {
		commonAncestorHeight = oldestCached - 1
		commonAncestorHash = "unknown (exceeded local history window)"
	}

	depth := len(orphaned)

	// Update local cache: replace orphaned blocks with canonical blocks
	for _, cBlk := range canonical {
		cache.Put(cBlk)
	}

	return &ReorgEvent{
		Blockchain:      chain,
		DetectedAt:      time.Now(),
		Depth:           depth,
		ForkHeight:      commonAncestorHeight,
		ForkHash:        commonAncestorHash,
		OrphanedBlocks:  orphaned,
		CanonicalBlocks: canonical,
	}, nil
}

// fetchBlock retrieves block details by height from GET /{blockchain}/block/{height}.
func fetchBlock(ctx context.Context, chain string, height uint64, apiKey string) (*BlockResponse, error) {
	reqURL := fmt.Sprintf("%s/%s/block/%d", apiBaseURL, chain, height)
	var resp BlockResponse
	err := fetchJSON(ctx, reqURL, apiKey, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// getBlockchainStatus retrieves current network height from GET /{blockchain}/status.
func getBlockchainStatus(ctx context.Context, chain, apiKey string) (*BlockchainStatus, error) {
	reqURL := fmt.Sprintf("%s/%s/status", apiBaseURL, chain)
	var status BlockchainStatus
	err := fetchJSON(ctx, reqURL, apiKey, &status)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// fetchJSON executes an HTTP GET request with retries and handles API rate limits / errors.
func fetchJSON(ctx context.Context, reqURL, apiKey string, target any) error {
	if !strings.HasPrefix(reqURL, "http") {
		reqURL = apiBaseURL + reqURL
	}

	u, err := url.Parse(reqURL)
	if err != nil {
		return err
	}

	if apiKey != "" && apiKey != "YOUR_API_KEY_HERE" && u.Query().Get("api_key") == "" {
		q := u.Query()
		q.Set("api_key", apiKey)
		u.RawQuery = q.Encode()
	}

	var lastErr error
	backoff := initialBackoff

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return err
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
		} else {
			if resp.StatusCode == http.StatusOK {
				err := json.NewDecoder(resp.Body).Decode(target)
				_ = resp.Body.Close()
				return err
			}

			bodyBytes, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			bodyStr := strings.TrimSpace(string(bodyBytes))

			if resp.StatusCode == http.StatusNotFound {
				return ErrNotFound
			}

			if bodyStr != "" {
				lastErr = fmt.Errorf("HTTP %d (%s): %s", resp.StatusCode, resp.Status, bodyStr)
			} else {
				lastErr = fmt.Errorf("HTTP %d (%s)", resp.StatusCode, resp.Status)
			}

			// Fatal client errors (400, 401, 403) are not retryable
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return lastErr
			}

			// Only announce a retry when one is actually coming.
			if attempt < maxRetries {
				if resp.StatusCode == http.StatusTooManyRequests {
					log.Printf("Rate limit (HTTP 429). Retrying attempt %d/%d in %v...", attempt, maxRetries, backoff)
				} else {
					log.Printf("Server error (HTTP %d). Retrying attempt %d/%d in %v...", resp.StatusCode, attempt, maxRetries, backoff)
				}
			}
		}

		// Only back off between attempts. Sleeping after the final attempt just
		// delays the error the caller is already going to get.
		if attempt < maxRetries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
			}
		}
	}

	return fmt.Errorf("request failed after %d retries: %w", maxRetries, lastErr)
}

// sleepContext pauses execution for d duration while remaining cancellable.
func sleepContext(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// resolveAPIKey inspects CLI flags, environment variables, and fallback constants.
func resolveAPIKey(flagKey string) string {
	if flagKey != "" {
		return flagKey
	}
	if envKey := os.Getenv("WHALE_ALERT_API_KEY"); envKey != "" {
		return envKey
	}
	return whaleAlertApiKey
}

// printHeader displays the startup dashboard banner.
func printHeader(chain string, startHeight uint64, historySize int, poll time.Duration) {
	fmt.Println()
	fmt.Println("===================================================================================")
	fmt.Printf("           WHALE ALERT CHAIN REORGANIZATION & ORPHAN DETECTOR (%s)\n", strings.ToUpper(chain))
	fmt.Println("===================================================================================")
	fmt.Printf("Blockchain Network  : %s\n", strings.ToUpper(chain))
	fmt.Printf("Starting Height     : #%s\n", humanize.Comma(int64(startHeight)))
	fmt.Printf("Sliding Cache Window: %d blocks\n", historySize)
	fmt.Printf("Polling Interval    : %v\n", poll)
	fmt.Println("===================================================================================")
}

// printBlockTick logs a single canonical block confirmation.
func printBlockTick(b CachedBlock, stats *TrackerStats) {
	blockTime := time.Unix(b.Timestamp, 0).UTC()
	age := time.Since(blockTime).Round(time.Second)

	fmt.Printf("[%s] Block #%-10s | Hash: %s | %3d txs | Mined: %s ago | Reorgs: %d\n",
		time.Now().UTC().Format("15:04:05"),
		humanize.Comma(int64(b.Height)),
		formatShortHash(b.Hash),
		b.TxCount,
		age,
		stats.ReorgEventsCount,
	)
}

// printReorgAlert prints an eye-catching alert detailing a detected chain reorg.
func printReorgAlert(e *ReorgEvent) {
	fmt.Println()
	fmt.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	fmt.Println("⚠️  ALERT: BLOCKCHAIN REORGANIZATION (REORG / ORPHAN) DETECTED!")
	fmt.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	fmt.Printf("Detected At         : %s\n", e.DetectedAt.UTC().Format("2006-01-02 15:04:05.000 UTC"))
	fmt.Printf("Blockchain Network  : %s\n", strings.ToUpper(e.Blockchain))
	fmt.Printf("Reorganization Depth: %d BLOCK(S)\n", e.Depth)
	fmt.Printf("Common Ancestor     : Block #%s (Hash: %s)\n",
		humanize.Comma(int64(e.ForkHeight)), formatShortHash(e.ForkHash))
	fmt.Println("-----------------------------------------------------------------------------------")
	fmt.Println("DISCARDED / ORPHANED BLOCKS (Old Branch):")
	for i, ob := range e.OrphanedBlocks {
		fmt.Printf("  [%d] Height #%-10s | Orphan Hash   : %s (%d txs)\n",
			i+1, humanize.Comma(int64(ob.Height)), ob.Hash, ob.TxCount)
	}
	fmt.Println("-----------------------------------------------------------------------------------")
	fmt.Println("NEW CANONICAL REPLACEMENT BLOCKS (New Branch):")
	for i, cb := range e.CanonicalBlocks {
		fmt.Printf("  [%d] Height #%-10s | Canonical Hash: %s (%d txs)\n",
			i+1, humanize.Comma(int64(cb.Height)), cb.Hash, cb.TxCount)
	}
	fmt.Println("===================================================================================")
	fmt.Println()
}

// printSummaryReport prints overall operational statistics upon shutdown.
func printSummaryReport(chain string, stats *TrackerStats, cache *BlockCache) {
	duration := time.Since(stats.StartTime).Round(time.Second)

	fmt.Println()
	fmt.Println("===================================================================================")
	fmt.Printf("                   REORG DETECTOR SUMMARY REPORT (%s)\n", strings.ToUpper(chain))
	fmt.Println("===================================================================================")
	fmt.Printf("Total Elapsed Time  : %s\n", duration)
	fmt.Printf("Blocks Monitored    : %s\n", humanize.Comma(int64(stats.BlocksScanned)))
	fmt.Printf("Reorg Events Count  : %d\n", stats.ReorgEventsCount)
	fmt.Printf("Total Orphan Blocks : %d\n", stats.TotalOrphanBlocks)
	fmt.Printf("Max Reorg Depth     : %d block(s)\n", stats.MaxReorgDepth)
	if oldest, ok := cache.OldestHeight(); ok {
		latest, _ := cache.LatestHeight()
		fmt.Printf("Active Cache Window : #%s to #%s (%d blocks)\n",
			humanize.Comma(int64(oldest)), humanize.Comma(int64(latest)), cache.Len())
	}
	fmt.Println("===================================================================================")
}

// formatShortHash formats full 64-character hashes for clean console presentation.
func formatShortHash(hash string) string {
	if len(hash) > 18 {
		return hash[:10] + "..." + hash[len(hash)-8:]
	}
	return hash
}
