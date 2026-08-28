package main

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
)

const (
	// Whale Alert API Key.
	// Replace this value or set the WHALE_ALERT_API_KEY environment variable.
	whaleAlertApiKey = "YOUR_API_KEY_HERE"

	apiBaseURL        = "https://leviathan.whale-alert.io"
	defaultBlockchain = "ethereum"
	defaultBlockRange = uint64(10)
	defaultLimit      = 256
	defaultMinUSD     = 100_000.0

	httpTimeout    = 15 * time.Second
	maxRetries     = 3
	initialBackoff = 1 * time.Second
)

// HTTP client with configured timeout
var httpClient = &http.Client{
	Timeout: httpTimeout,
}

// BlockchainStatus represents the response from GET /{blockchain}/status
type BlockchainStatus struct {
	EndHeight uint64 `json:"end_height"`
}

// TransactionsResponse represents the response from GET /{blockchain}/transactions
type TransactionsResponse struct {
	Transactions []Transaction `json:"transactions"`
	Next         string        `json:"next"`
}

// Transaction represents a single on-chain transaction (identified by hash and block height).
// A single transaction may contain multiple sub-transactions.
type Transaction struct {
	BlockHeight     uint64           `json:"height"`
	IndexInBlock    uint64           `json:"index_in_block"`
	Timestamp       int64            `json:"timestamp"`
	Hash            string           `json:"hash"`
	Fee             string           `json:"fee,omitempty"`
	FeeSymbol       string           `json:"fee_symbol,omitempty"`
	FeeSymbolPrice  float64          `json:"fee_symbol_price,omitempty"`
	SubTransactions []SubTransaction `json:"sub_transactions"`
}

// SubTransaction represents an individual token or coin movement within a transaction.
// For example, smart contract calls, batch payouts, or multi-token swaps produce multiple sub-transactions.
type SubTransaction struct {
	Symbol          string    `json:"symbol"`
	TransactionType string    `json:"transaction_type"`
	Price           float64   `json:"unit_price_usd"`
	Inputs          []Account `json:"inputs"`
	Outputs         []Account `json:"outputs"`
}

// Account represents a sender (input) or recipient (output), including Whale Alert entity attribution.
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

// SymbolStats holds aggregated transfer count, coin volume, and USD valuation per cryptocurrency symbol.
type SymbolStats struct {
	Symbol      string
	Count       int
	TotalAmount float64
	VolumeUSD   float64
}

// TopTransaction stores metadata for high-value transfer ranking and console display.
type TopTransaction struct {
	Hash        string
	BlockHeight uint64
	Timestamp   int64
	Symbol      string
	Amount      float64
	AmountUSD   float64
	Type        string
	FromOwner   string
	ToOwner     string
}

// AggregatedMetrics holds all historical metrics collected across the queried block height range.
type AggregatedMetrics struct {
	StartBlock      uint64
	EndBlock        uint64
	MinTimestamp    int64
	MaxTimestamp    int64
	TotalTxCount    int
	TotalSubTxCount int
	TotalVolumeUSD  float64
	SymbolStats     map[string]*SymbolStats
	TypeStats       map[string]int
	UniqueSenders   map[string]struct{}
	UniqueReceivers map[string]struct{}
	TopTransfers    []TopTransaction
}

func main() {
	// 1. Parse command-line flags
	blockchainFlag := flag.String("blockchain", defaultBlockchain, "Target blockchain (e.g. ethereum, bitcoin, tron)")
	startBlockFlag := flag.Uint64("start", 0, "Starting block height (0 to calculate using -blocks offset)")
	endBlockFlag := flag.Uint64("end", 0, "Ending block height (0 for latest network block)")
	blockRangeFlag := flag.Uint64("blocks", defaultBlockRange, "Block height range count (used if -start is 0)")
	limitFlag := flag.Int("limit", defaultLimit, "Maximum number of transactions to retrieve per page")
	minUSDFlag := flag.Float64("min-usd", defaultMinUSD, "Minimum transaction value in USD to include in analysis")
	apiKeyFlag := flag.String("api-key", "", "Whale Alert API key (defaults to WHALE_ALERT_API_KEY env var)")

	flag.Parse()

	apiKey := resolveAPIKey(*apiKeyFlag)
	if apiKey == "" || apiKey == "YOUR_API_KEY_HERE" {
		log.Fatal("Warning: WHALE_ALERT_API_KEY is not set. REST historical queries require an active API key.")
	}

	// Listen for Ctrl+C or termination signals for clean, graceful exit
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	chain := strings.ToLower(*blockchainFlag)
	log.Printf("Initializing Historical Query for blockchain: %s\n", chain)

	// 2. Resolve block height range:
	// If -end is not specified, query GET /{blockchain}/status for the latest confirmed block height.
	endBlock := *endBlockFlag
	if endBlock == 0 {
		latestHeight, err := getLatestHeight(ctx, chain, apiKey)
		if err != nil {
			log.Fatalf("Failed to fetch latest block height for %s: %v", chain, err)
		}
		endBlock = latestHeight
	}

	// If -start is not specified, compute it as (endBlock - blocks + 1)
	startBlock := *startBlockFlag
	if startBlock == 0 {
		if endBlock >= *blockRangeFlag {
			startBlock = endBlock - *blockRangeFlag + 1
		} else {
			startBlock = 1
		}
	}

	if startBlock > endBlock {
		log.Fatalf("Invalid block height range: start_height (#%d) is greater than end_height (#%d)", startBlock, endBlock)
	}

	log.Printf("Querying block height range: #%s to #%s (%s blocks) [Threshold: >= $%s USD]\n",
		humanize.Comma(int64(startBlock)),
		humanize.Comma(int64(endBlock)),
		humanize.Comma(int64(endBlock-startBlock+1)),
		humanize.CommafWithDigits(*minUSDFlag, 0),
	)

	metrics := &AggregatedMetrics{
		StartBlock:      startBlock,
		EndBlock:        endBlock,
		SymbolStats:     make(map[string]*SymbolStats),
		TypeStats:       make(map[string]int),
		UniqueSenders:   make(map[string]struct{}),
		UniqueReceivers: make(map[string]struct{}),
	}

	limit := *limitFlag
	if limit <= 0 {
		limit = defaultLimit
	}

	// 3. Construct initial REST query URL:
	// GET /{blockchain}/transactions?start_height=...&end_height=...&min_value=...&limit=...
	currentURL := fmt.Sprintf("%s/%s/transactions?start_height=%d&end_height=%d&min_value=%d&limit=%d",
		apiBaseURL, chain, startBlock, endBlock, int64(*minUSDFlag), limit)

	startTime := time.Now()
	pageCount := 0

	// 4. Cursor-based pagination loop:
	// The Whale Alert API returns a `next` URL string containing the pagination cursor.
	// When `next` is empty or matches currentURL, the scan across the block range is complete.
	for currentURL != "" && ctx.Err() == nil {
		pageCount++
		log.Printf("Fetching page %d...", pageCount)

		resp, err := fetchTransactions(ctx, currentURL, apiKey)
		if err != nil {
			log.Printf("Error fetching transaction page %d: %v", pageCount, err)
			break
		}

		if len(resp.Transactions) == 0 {
			log.Printf("Page %d returned 0 transactions. Historical scan complete.", pageCount)
			break
		}

		minBlock := resp.Transactions[0].BlockHeight
		maxBlock := resp.Transactions[len(resp.Transactions)-1].BlockHeight

		log.Printf("Processing page %d (%d transactions received, blocks #%s to #%s)...",
			pageCount, len(resp.Transactions),
			humanize.Comma(int64(minBlock)),
			humanize.Comma(int64(maxBlock)),
		)

		processTransactions(resp.Transactions, metrics, *minUSDFlag)

		if resp.Next != "" && resp.Next != currentURL {
			currentURL = resp.Next
		} else {
			currentURL = ""
		}
	}

	duration := time.Since(startTime)

	// 5. Display comprehensive historical summary report
	printReport(chain, metrics, *minUSDFlag, duration, pageCount)
}

// resolveAPIKey inspects CLI flags, environment variables, and fallback constants in priority order.
func resolveAPIKey(flagKey string) string {
	if flagKey != "" {
		return flagKey
	}
	if envKey := os.Getenv("WHALE_ALERT_API_KEY"); envKey != "" {
		return envKey
	}
	return whaleAlertApiKey
}

// fetchJSON executes an HTTP GET request, injects the API key parameter, handles retry backoff
// for rate limits (HTTP 429) and server errors (HTTP 5xx), and decodes the JSON payload into target.
func fetchJSON(ctx context.Context, reqURL, apiKey string, target any) error {
	if !strings.HasPrefix(reqURL, "http") {
		reqURL = apiBaseURL + reqURL
	}

	u, err := url.Parse(reqURL)
	if err != nil {
		return err
	}

	if apiKey != "" && u.Query().Get("api_key") == "" {
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

			if bodyStr != "" {
				lastErr = fmt.Errorf("HTTP %d (%s): %s", resp.StatusCode, resp.Status, bodyStr)
			} else {
				lastErr = fmt.Errorf("HTTP %d (%s)", resp.StatusCode, resp.Status)
			}

			// Fatal client errors (400 Bad Request, 401 Unauthorized, 403 Forbidden) are not retryable
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return lastErr
			}

			// Only announce a retry when one is actually coming.
			if attempt < maxRetries {
				if resp.StatusCode == http.StatusTooManyRequests {
					log.Printf("Rate limit encountered (HTTP 429). Retrying attempt %d/%d in %v...", attempt, maxRetries, backoff)
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

// getLatestHeight fetches the newest confirmed block height from GET /{blockchain}/status.
func getLatestHeight(ctx context.Context, chain, apiKey string) (uint64, error) {
	var status BlockchainStatus
	err := fetchJSON(ctx, fmt.Sprintf("/%s/status", chain), apiKey, &status)
	return status.EndHeight, err
}

// fetchTransactions retrieves a page of transactions from GET /{blockchain}/transactions.
func fetchTransactions(ctx context.Context, fetchURL, apiKey string) (*TransactionsResponse, error) {
	var resp TransactionsResponse
	err := fetchJSON(ctx, fetchURL, apiKey, &resp)
	return &resp, err
}

// processTransactions filters and aggregates a batch of transactions into statistical metrics.
//
// API Understanding:
//   - A single on-chain `Transaction` can encompass multiple `SubTransactions` (e.g. multi-token contract interactions).
//   - Each `SubTransaction` represents an individual asset movement with its own symbol, unit price, inputs, and outputs.
//   - Amounts are provided as strings in JSON to preserve precision across varying token decimals.
//   - For standard transfers/mints, transferred value is in `outputs`. For `burn` events where tokens are destroyed
//     without destination outputs, we fallback to summing `inputs`.
func processTransactions(txs []Transaction, metrics *AggregatedMetrics, minUSD float64) {
	for _, tx := range txs {
		var hasMatchingSubTx bool

		for _, sub := range tx.SubTransactions {
			symUpper := strings.ToUpper(sub.Symbol)
			txType := strings.ToLower(sub.TransactionType)

			// Parse token amount: check outputs first, fallback to inputs for burns
			amount := sumAmounts(sub.Outputs)
			if amount == 0 {
				amount = sumAmounts(sub.Inputs)
			}
			if amount <= 0 {
				continue
			}

			// Calculate USD fiat valuation using the unit price at the time of the transaction
			var amountUSD float64
			if sub.Price > 0 {
				amountUSD = amount * sub.Price
			}

			// Filter out transactions below the USD threshold to focus on high-value whale movements
			if amountUSD < minUSD {
				continue
			}

			hasMatchingSubTx = true
			metrics.TotalSubTxCount++
			metrics.TypeStats[txType]++

			// Track volume and count per cryptocurrency symbol
			stat, ok := metrics.SymbolStats[symUpper]
			if !ok {
				stat = &SymbolStats{Symbol: symUpper}
				metrics.SymbolStats[symUpper] = stat
			}
			stat.Count++
			stat.TotalAmount += amount
			stat.VolumeUSD += amountUSD
			metrics.TotalVolumeUSD += amountUSD

			// Deduplicate unique addresses and extract known entity owner labels (e.g., "binance", "coinbase")
			fromOwner := extractParty(sub.Inputs, metrics.UniqueSenders)
			toOwner := extractParty(sub.Outputs, metrics.UniqueReceivers)

			// Record qualifying transfer for top-value ranking
			metrics.TopTransfers = append(metrics.TopTransfers, TopTransaction{
				Hash:        tx.Hash,
				BlockHeight: tx.BlockHeight,
				Timestamp:   tx.Timestamp,
				Symbol:      symUpper,
				Amount:      amount,
				AmountUSD:   amountUSD,
				Type:        txType,
				FromOwner:   fromOwner,
				ToOwner:     toOwner,
			})
		}

		// If at least one sub-transaction qualified, update parent transaction count and timestamp window
		if hasMatchingSubTx {
			metrics.TotalTxCount++
			if metrics.MinTimestamp == 0 || tx.Timestamp < metrics.MinTimestamp {
				metrics.MinTimestamp = tx.Timestamp
			}
			if tx.Timestamp > metrics.MaxTimestamp {
				metrics.MaxTimestamp = tx.Timestamp
			}
		}
	}
}

// sumAmounts parses and sums positive float amounts from a slice of accounts.
func sumAmounts(accounts []Account) float64 {
	var total float64
	for _, acc := range accounts {
		if a, err := strconv.ParseFloat(acc.Amount, 64); err == nil && a > 0 {
			total += a
		}
	}
	return total
}

// extractParty records unique addresses into the set and returns the primary entity owner label if known.
func extractParty(accounts []Account, addressSet map[string]struct{}) string {
	owner := "unknown"
	for _, acc := range accounts {
		if acc.Address != "" {
			addressSet[acc.Address] = struct{}{}
		}
		if acc.Owner != "" && owner == "unknown" {
			owner = acc.Owner
		}
	}
	return owner
}

// printReport displays a clean, formatted ASCII report summarizing the historical scan results.
func printReport(chain string, metrics *AggregatedMetrics, minUSD float64, duration time.Duration, pages int) {
	fmt.Println("\n===================================================================================")
	fmt.Printf("               WHALE ALERT HISTORICAL QUERY REPORT (%s)\n", strings.ToUpper(chain))
	fmt.Println("===================================================================================")
	fmt.Printf("Block Height Range  : #%s to #%s (%s blocks)\n",
		humanize.Comma(int64(metrics.StartBlock)),
		humanize.Comma(int64(metrics.EndBlock)),
		humanize.Comma(int64(metrics.EndBlock-metrics.StartBlock+1)),
	)

	if metrics.MinTimestamp > 0 && metrics.MaxTimestamp > 0 {
		startT := time.Unix(metrics.MinTimestamp, 0).UTC().Format("2006-01-02 15:04:05 UTC")
		endT := time.Unix(metrics.MaxTimestamp, 0).UTC().Format("2006-01-02 15:04:05 UTC")
		fmt.Printf("Time Span           : %s to %s\n", startT, endT)
	}

	fmt.Printf("USD Threshold       : >= $%s USD\n", humanize.CommafWithDigits(minUSD, 0))
	fmt.Printf("Scan Performance    : Processed %d pages in %s\n", pages, duration.Round(time.Millisecond))
	fmt.Println("-----------------------------------------------------------------------------------")
	fmt.Printf("Total Transactions  : %s transactions (%s sub-transactions)\n",
		humanize.Comma(int64(metrics.TotalTxCount)),
		humanize.Comma(int64(metrics.TotalSubTxCount)),
	)
	fmt.Printf("Total Volume USD    : $%s USD\n", humanize.CommafWithDigits(metrics.TotalVolumeUSD, 2))
	fmt.Printf("Unique Addresses    : %s senders, %s receivers\n",
		humanize.Comma(int64(len(metrics.UniqueSenders))),
		humanize.Comma(int64(len(metrics.UniqueReceivers))),
	)

	// 1. Volume Breakdown by Symbol
	if len(metrics.SymbolStats) > 0 {
		fmt.Println("\n-----------------------------------------------------------------------------------")
		fmt.Println(" VOLUME BREAKDOWN BY SYMBOL")
		fmt.Println("-----------------------------------------------------------------------------------")
		fmt.Printf("%-10s | %-12s | %-24s | %-24s\n", "SYMBOL", "TRANSFERS", "TOTAL AMOUNT", "TOTAL VOLUME (USD)")
		fmt.Println("-----------+--------------+--------------------------+-----------------------------")

		sortedSymbols := make([]*SymbolStats, 0, len(metrics.SymbolStats))
		for _, s := range metrics.SymbolStats {
			sortedSymbols = append(sortedSymbols, s)
		}
		slices.SortFunc(sortedSymbols, func(a, b *SymbolStats) int {
			return cmp.Compare(b.VolumeUSD, a.VolumeUSD)
		})

		for _, s := range sortedSymbols {
			fmt.Printf("%-10s | %-12s | %-24s | $%s\n",
				s.Symbol,
				humanize.Comma(int64(s.Count)),
				humanize.CommafWithDigits(s.TotalAmount, 4),
				humanize.CommafWithDigits(s.VolumeUSD, 2),
			)
		}
	}

	// 2. Breakdown by Transaction Type
	if len(metrics.TypeStats) > 0 {
		fmt.Println("\n-----------------------------------------------------------------------------------")
		fmt.Println(" TRANSACTION TYPES")
		fmt.Println("-----------------------------------------------------------------------------------")
		types := make([]string, 0, len(metrics.TypeStats))
		for t := range metrics.TypeStats {
			types = append(types, t)
		}
		slices.SortFunc(types, func(a, b string) int {
			if c := cmp.Compare(metrics.TypeStats[b], metrics.TypeStats[a]); c != 0 {
				return c
			}
			return cmp.Compare(a, b)
		})
		for _, t := range types {
			fmt.Printf(" - %-12s : %s\n", formatTitle(t), humanize.Comma(int64(metrics.TypeStats[t])))
		}
	}

	// 3. Top 5 Highest Value Transfers
	if len(metrics.TopTransfers) > 0 {
		slices.SortFunc(metrics.TopTransfers, func(a, b TopTransaction) int {
			return cmp.Compare(b.AmountUSD, a.AmountUSD)
		})

		topN := min(len(metrics.TopTransfers), 5)

		fmt.Println("\n-----------------------------------------------------------------------------------")
		fmt.Printf(" TOP %d HIGHEST VALUE TRANSFERS\n", topN)
		fmt.Println("-----------------------------------------------------------------------------------")
		for i := range topN {
			tt := metrics.TopTransfers[i]
			fmt.Printf("#%d | Block #%d | %s %s ($%s USD)\n",
				i+1,
				tt.BlockHeight,
				humanize.CommafWithDigits(tt.Amount, 2),
				tt.Symbol,
				humanize.CommafWithDigits(tt.AmountUSD, 2),
			)
			fmt.Printf("    Type: %-8s | From: %-15s -> To: %-15s | Hash: %s\n",
				tt.Type, tt.FromOwner, tt.ToOwner, formatShortHash(tt.Hash),
			)
		}
	}

	fmt.Println("===================================================================================")
}

// formatShortHash truncates 64-character transaction hashes for clean console tabular display.
func formatShortHash(hash string) string {
	if len(hash) > 16 {
		return hash[:8] + "..." + hash[len(hash)-8:]
	}
	return hash
}

// formatTitle converts an all-lowercase string into title-cased format (e.g. "transfer" -> "Transfer").
func formatTitle(s string) string {
	if len(s) == 0 {
		return ""
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}
