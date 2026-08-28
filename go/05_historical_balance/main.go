package main

import (
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
	defaultAddress    = "0xbdb3ba9ffe392549e1f8658dd2630c141fdf47b6"
	defaultSymbol     = "ETH"

	httpTimeout    = 30 * time.Second
	maxRetries     = 3
	initialBackoff = 1 * time.Second
)

var httpClient = &http.Client{
	Timeout: httpTimeout,
}

// BlockchainStatus represents the response from GET /{blockchain}/status
type BlockchainStatus struct {
	StartHeight   uint64 `json:"start_height"`
	EndHeight     uint64 `json:"end_height"`
	MinPlanHeight uint64 `json:"min_plan_height"`
}

// HeightAtTimeResponse represents the response from GET /{blockchain}/height_at_time/{timestamp}
type HeightAtTimeResponse struct {
	Height    uint64 `json:"height"`
	Timestamp int64  `json:"timestamp"`
}

// TransactionsResponse represents the response from GET /{blockchain}/transactions
type TransactionsResponse struct {
	Transactions []Transaction `json:"transactions"`
	Next         string        `json:"next,omitempty"`
}

// Transaction represents a blockchain transaction
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

// SubTransaction represents asset movement and price metrics within a transaction
type SubTransaction struct {
	Symbol          string    `json:"symbol"`
	TransactionType string    `json:"transaction_type"`
	UnitPriceUSD    float64   `json:"unit_price_usd"`
	Inputs          []Account `json:"inputs"`
	Outputs         []Account `json:"outputs"`
}

// Account represents a sender (input) or receiver (output) with post-transaction balance
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

// BalanceSnapshot stores the resolved balance and price at a historical point in time
type BalanceSnapshot struct {
	Blockchain    string
	Address       string
	Owner         string
	OwnerType     string
	Symbol        string
	TargetTime    time.Time
	TargetBlock   uint64
	BlockTime     time.Time
	Found         bool
	Balance       string
	BalanceFloat  float64
	UnitPriceUSD  float64
	TotalValueUSD float64
}

func main() {
	// 1. Parse CLI flags
	blockchainFlag := flag.String("blockchain", defaultBlockchain, "Target blockchain network (e.g. ethereum, bitcoin, tron, solana)")
	addressFlag := flag.String("address", defaultAddress, "Target wallet or smart contract address")
	symbolFlag := flag.String("symbol", defaultSymbol, "Target token or coin symbol (e.g. USDT, ETH, USDC, BTC)")
	timeFlag := flag.String("time", "", "Historical point in time (Unix timestamp, RFC3339 string like 2026-08-25T10:00:00Z, or duration like 1h, 24h, 7d). Defaults to 1 hour ago.")
	startBlockFlag := flag.Uint64("start", 0, "Starting block height (defaults to 0 / plan minimum height)")
	apiKeyFlag := flag.String("api-key", "", "Whale Alert API key (defaults to WHALE_ALERT_API_KEY env var)")

	flag.Parse()

	apiKey := resolveAPIKey(*apiKeyFlag)
	if apiKey == "" || apiKey == "YOUR_API_KEY_HERE" {
		log.Fatal("WHALE_ALERT_API_KEY is not set. Historical queries require an active API key. Set the environment variable, pass -api-key, or edit the constant in main.go.")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("\nCancellation signal received. Exiting...")
		cancel()
	}()

	chain := strings.ToLower(strings.TrimSpace(*blockchainFlag))
	targetAddr := strings.TrimSpace(*addressFlag)
	targetSym := strings.ToUpper(strings.TrimSpace(*symbolFlag))

	if targetAddr == "" {
		log.Fatal("Error: target address (-address) cannot be empty.")
	}
	if targetSym == "" {
		log.Fatal("Error: target symbol (-symbol) cannot be empty.")
	}

	// 2. Parse target historical timestamp
	targetTime, err := parseTargetTime(*timeFlag)
	if err != nil {
		log.Fatalf("Invalid time parameter %q: %v", *timeFlag, err)
	}

	unixTimestamp := targetTime.Unix()

	log.Println("===================================================================================")
	log.Println("            WHALE ALERT HISTORICAL BALANCE LOOKUP (POINT IN TIME)")
	log.Println("===================================================================================")
	log.Printf("Blockchain      : %s\n", chain)
	log.Printf("Target Address  : %s\n", targetAddr)
	log.Printf("Target Symbol   : %s\n", targetSym)
	log.Printf("Point in Time   : %s (Unix: %d)\n", targetTime.UTC().Format(time.RFC3339), unixTimestamp)
	log.Println("-----------------------------------------------------------------------------------")

	// 3. Step 1: Resolve block height at timestamp via GET /{blockchain}/height_at_time/{timestamp}
	log.Printf("[1/3] Resolving block height at %s via GET /%s/height_at_time/%d...",
		targetTime.UTC().Format("2006-01-02 15:04:05 UTC"), chain, unixTimestamp)

	var heightResp HeightAtTimeResponse
	if err := fetchJSON(ctx, fmt.Sprintf("%s/%s/height_at_time/%d", apiBaseURL, chain, unixTimestamp), apiKey, &heightResp); err != nil {
		log.Fatalf("Failed to resolve block height at time: %v", err)
	}

	targetBlock := heightResp.Height
	blockTime := time.Unix(heightResp.Timestamp, 0).UTC()
	timeDelta := targetTime.Sub(blockTime)

	log.Printf("      -> Resolved Block Height #%s (mined %s, delta: %v)",
		humanize.Comma(int64(targetBlock)), blockTime.Format("2006-01-02 15:04:05 UTC"), timeDelta.Round(time.Second))

	// 4. Step 2: Check blockchain plan boundaries via GET /{blockchain}/status
	// Note: The Enterprise plan retains the last 90 days of history (Enterprise Plus provides full historical access).
	// Because balance resolution retrieves the address's last transaction, if an address had no transactions for that
	// symbol within the last 90 days, its historical balance cannot be resolved.
	log.Printf("[2/3] Checking blockchain plan boundaries via GET /%s/status...", chain)
	var status BlockchainStatus
	startBlock := *startBlockFlag

	if err := fetchJSON(ctx, fmt.Sprintf("%s/%s/status", apiBaseURL, chain), apiKey, &status); err != nil {
		log.Printf("Warning: Unable to verify plan boundaries: %v", err)
	} else {
		log.Printf("      -> Current Height: #%s | Minimum Plan Height: #%s",
			humanize.Comma(int64(status.EndHeight)), humanize.Comma(int64(status.MinPlanHeight)))

		if status.MinPlanHeight > 0 && targetBlock < status.MinPlanHeight {
			log.Fatalf("Error: Target block #%s is below the minimum allowed height #%s for this API plan.",
				humanize.Comma(int64(targetBlock)), humanize.Comma(int64(status.MinPlanHeight)))
		}

		if startBlock < status.MinPlanHeight {
			startBlock = status.MinPlanHeight
		}
	}

	if startBlock > targetBlock {
		log.Fatalf("Error: Start block #%s cannot be greater than target block #%s.",
			humanize.Comma(int64(startBlock)), humanize.Comma(int64(targetBlock)))
	}

	// 5. Step 3: Fetch the latest transaction for the address/symbol up to the target block
	log.Printf("[3/3] Querying address balance (order=desc, limit=1) up to block #%s...",
		humanize.Comma(int64(targetBlock)))

	snapshot, err := fetchAddressBalanceAtBlock(ctx, chain, targetAddr, targetSym, startBlock, targetBlock, targetTime, blockTime, apiKey)
	if err != nil {
		log.Fatalf("Failed to fetch address balance: %v", err)
	}

	// 6. Display formatted result report
	printBalanceReport(snapshot)
}

// parseTargetTime handles Unix epoch seconds, RFC3339 strings, relative durations (e.g. '1h', '24h', '7d'), or defaults to 1 hour ago.
func parseTargetTime(input string) (time.Time, error) {
	input = strings.TrimSpace(input)
	if input == "" || input == "0" {
		return time.Now().Add(-1 * time.Hour), nil
	}

	// Handle day suffix (e.g. "1d", "7d", "30d")
	if strings.HasSuffix(strings.ToLower(input), "d") {
		if days, err := strconv.ParseFloat(input[:len(input)-1], 64); err == nil {
			return time.Now().Add(-time.Duration(days * 24 * float64(time.Hour))), nil
		}
	}

	// Relative duration (e.g. "1h", "24h", "30m", "72h")
	if dur, err := time.ParseDuration(input); err == nil {
		if dur > 0 {
			dur = -dur
		}
		return time.Now().Add(dur), nil
	}

	// Unix timestamp (seconds)
	if ts, err := strconv.ParseInt(input, 10, 64); err == nil {
		return time.Unix(ts, 0), nil
	}

	// RFC3339 / ISO date formats
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, input); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unrecognized time format (expected Unix timestamp, RFC3339 timestamp, or duration like '1h', '7d')")
}

// fetchAddressBalanceAtBlock queries GET /{blockchain}/transactions with order=desc&limit=1 to extract the address's balance
func fetchAddressBalanceAtBlock(
	ctx context.Context,
	chain, address, symbol string,
	startBlock, endBlock uint64,
	targetTime, blockTime time.Time,
	apiKey string,
) (*BalanceSnapshot, error) {
	reqURL := fmt.Sprintf("%s/%s/transactions?start_height=%d&end_height=%d&address=%s&symbol=%s&order=desc&limit=1",
		apiBaseURL, chain, startBlock, endBlock, url.QueryEscape(address), url.QueryEscape(symbol))

	var txResp TransactionsResponse
	if err := fetchJSON(ctx, reqURL, apiKey, &txResp); err != nil {
		return nil, err
	}

	snapshot := &BalanceSnapshot{
		Blockchain:  chain,
		Address:     address,
		Symbol:      symbol,
		TargetTime:  targetTime,
		TargetBlock: endBlock,
		BlockTime:   blockTime,
	}

	if len(txResp.Transactions) == 0 {
		return snapshot, nil
	}

	tx := txResp.Transactions[0]

	// Locate the address within the matching sub-transaction to extract its post-transaction balance
	for _, sub := range tx.SubTransactions {
		if !strings.EqualFold(sub.Symbol, symbol) {
			continue
		}

		snapshot.UnitPriceUSD = sub.UnitPriceUSD

		// Search outputs first, then inputs for the target address balance.
		// Scan the two slices separately: appending Inputs onto Outputs would write
		// into the decoded payload's own backing array, which json leaves spare
		// capacity in for any slice of three or more elements.
		acc, ok := findAccount(sub.Outputs, address)
		if !ok {
			acc, ok = findAccount(sub.Inputs, address)
		}
		if !ok {
			continue
		}

		snapshot.Found = true
		snapshot.Balance = acc.Balance
		snapshot.Owner = acc.Owner
		snapshot.OwnerType = acc.OwnerType

		if balFloat, err := strconv.ParseFloat(acc.Balance, 64); err == nil {
			snapshot.BalanceFloat = balFloat
			snapshot.TotalValueUSD = balFloat * snapshot.UnitPriceUSD
		}
		return snapshot, nil
	}

	return snapshot, nil
}

// findAccount returns the first account whose address matches, compared case-insensitively.
func findAccount(accounts []Account, address string) (Account, bool) {
	for _, acc := range accounts {
		if strings.EqualFold(acc.Address, address) {
			return acc, true
		}
	}
	return Account{}, false
}

// fetchJSON executes an HTTP request and decodes the JSON payload into target.
func fetchJSON(ctx context.Context, reqURL, apiKey string, target any) error {
	if apiKey != "" && apiKey != "YOUR_API_KEY_HERE" {
		sep := "?"
		if strings.Contains(reqURL, "?") {
			sep = "&"
		}
		reqURL += sep + "api_key=" + url.QueryEscape(apiKey)
	}

	body, err := executeHTTPRequest(ctx, reqURL)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("JSON decode error: %w (body: %s)", err, string(body))
	}
	return nil
}

// executeHTTPRequest performs an HTTP request with retry backoff for rate limits and server errors.
func executeHTTPRequest(ctx context.Context, fetchURL string) ([]byte, error) {
	var lastErr error
	backoff := initialBackoff

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
		if err != nil {
			return nil, err
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
		} else {
			bodyBytes, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()

			if readErr != nil {
				lastErr = readErr
			} else if resp.StatusCode == http.StatusOK {
				return bodyBytes, nil
			} else {
				bodyStr := strings.TrimSpace(string(bodyBytes))
				if bodyStr != "" {
					lastErr = fmt.Errorf("HTTP %d (%s): %s", resp.StatusCode, resp.Status, bodyStr)
				} else {
					lastErr = fmt.Errorf("HTTP %d (%s)", resp.StatusCode, resp.Status)
				}

				if resp.StatusCode == http.StatusTooManyRequests {
					// Only announce a retry when one is actually coming.
					if attempt < maxRetries {
						log.Printf("Rate limit encountered (HTTP 429). Retrying attempt %d/%d in %v...", attempt, maxRetries, backoff)
					}
				} else if resp.StatusCode >= 500 {
					if attempt < maxRetries {
						log.Printf("Server error (HTTP %d). Retrying attempt %d/%d in %v...", resp.StatusCode, attempt, maxRetries, backoff)
					}
				} else {
					// Fatal client error (400, 401, 403, 404)
					return nil, lastErr
				}
			}
		}

		if attempt < maxRetries {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
			}
		}
	}

	return nil, fmt.Errorf("request failed after %d retries: %w", maxRetries, lastErr)
}

// printBalanceReport prints a clean, human-readable balance and price report at the target block.
func printBalanceReport(s *BalanceSnapshot) {
	fmt.Println()
	fmt.Println("===================================================================================")
	fmt.Println("                   BALANCE SNAPSHOT AT POINT IN TIME")
	fmt.Println("===================================================================================")
	fmt.Printf("Blockchain            : %s\n", strings.ToUpper(s.Blockchain))
	fmt.Printf("Address               : %s\n", s.Address)
	if s.Owner != "" {
		fmt.Printf("Owner Attribution     : %s (Type: %s)\n", s.Owner, s.OwnerType)
	}
	fmt.Printf("Asset Symbol          : %s\n", s.Symbol)
	fmt.Printf("Requested Time        : %s\n", s.TargetTime.UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Printf("Resolved Block Height : #%s (mined %s)\n",
		humanize.Comma(int64(s.TargetBlock)), s.BlockTime.Format("2006-01-02 15:04:05 UTC"))
	fmt.Println("-----------------------------------------------------------------------------------")

	if !s.Found {
		fmt.Println("STATUS: NO BALANCE FOUND")
		fmt.Printf("No balance record for address %s (%s) was found up to block #%s.\n",
			s.Address, s.Symbol, humanize.Comma(int64(s.TargetBlock)))
		fmt.Println("===================================================================================")
		return
	}

	fmt.Printf("BALANCE AT THAT TIME  : %s %s\n", humanize.CommafWithDigits(s.BalanceFloat, 4), s.Symbol)
	if s.UnitPriceUSD > 0 {
		fmt.Printf("ASSET PRICE (AT BLOCK): $%s USD\n", humanize.CommafWithDigits(s.UnitPriceUSD, 4))
		fmt.Printf("PORTFOLIO VALUE (USD) : $%s USD\n", humanize.CommafWithDigits(s.TotalValueUSD, 2))
	}
	fmt.Println("===================================================================================")
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
