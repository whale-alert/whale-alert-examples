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
	"sync"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
)

const (
	// Whale Alert API Key.
	// Replace this value or set the WHALE_ALERT_API_KEY environment variable.
	whaleAlertApiKey = "YOUR_API_KEY_HERE"

	apiBaseURL   = "https://leviathan.whale-alert.io"
	pollInterval = 3 * time.Second
	httpTimeout  = 15 * time.Second

	// Minimum USD threshold for alerts ($1,000 USD default)
	minUSDThreshold = 1_000.0
)

// Supported tracked stablecoins (lowercase symbol -> token name)
var stablecoins = map[string]string{
	"usdt":    "Tether",
	"usdc":    "USD Coin",
	"dai":     "Dai",
	"fei":     "Fei USD",
	"pax":     "Paxos Standard",
	"ust":     "TerraUSD",
	"busd":    "Binance USD",
	"eurt":    "Tether EUR",
	"gusd":    "Gemini Dollar",
	"husd":    "HUSD",
	"pusd":    "PUSD",
	"tusd":    "TrueUSD",
	"usdd":    "Decentralized USD",
	"usde":    "Ethena USDe",
	"usdg":    "Global Dollar",
	"usdh":    "USDH",
	"usdj":    "JUST Stablecoin",
	"usds":    "Sky Dollar",
	"usd1":    "USD1",
	"pyusd":   "PayPal USD",
	"rlusd":   "Ripple USD",
	"susds":   "Savings USDS",
	"pathusd": "PathUSD",
}

// HTTP client with configured timeout
var httpClient = &http.Client{
	Timeout: httpTimeout,
}

// StatusChain represents a blockchain network entry from GET /status
type StatusChain struct {
	Name    string   `json:"name"`
	Symbols []string `json:"symbols"`
	Height  uint64   `json:"height"`
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

// Transaction represents a single blockchain transaction
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

// SubTransaction represents token/coin movement within a transaction
type SubTransaction struct {
	Symbol          string    `json:"symbol"`
	TransactionType string    `json:"transaction_type"`
	Price           float64   `json:"unit_price_usd"`
	Inputs          []Account `json:"inputs"`
	Outputs         []Account `json:"outputs"`
}

// Account represents a sender (input) or receiver (output)
type Account struct {
	Address     string `json:"address"`
	Amount      string `json:"amount"`
	Balance     string `json:"balance,omitempty"`
	Locked      string `json:"locked,omitempty"`
	IsFrozen    bool   `json:"is_frozen,omitempty"`
	Owner       string `json:"owner"`
	OwnerType   string `json:"owner_type"`
	AddressType string `json:"address_type"`
}

// Mutex for synchronized console output across blockchain workers
var printMu sync.Mutex

func main() {
	apiKeyFlag := flag.String("api-key", "", "Whale Alert API key (defaults to WHALE_ALERT_API_KEY env var)")
	flag.Parse()

	apiKey := getAPIKey(*apiKeyFlag)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Printf("[%s] Stablecoin Mint, Burn, Freeze & Lock Monitor active (Threshold: $%s USD)...\n",
		time.Now().UTC().Format("2006-01-02 15:04:05"),
		humanize.CommafWithDigits(minUSDThreshold, 0),
	)

	// 1. Fetch available blockchains and symbols dynamically from the status endpoint
	allChains, err := fetchAvailableBlockchains(ctx, apiKey)
	if err != nil {
		log.Fatalf("Failed to fetch blockchain status: %v", err)
	}

	// 2. Filter blockchains that support our tracked stablecoins
	chainsToMonitor := getSupportedChains(allChains)
	if len(chainsToMonitor) == 0 {
		log.Fatalln("No blockchains found supporting the tracked stablecoins.")
	}

	chainNames := make([]string, 0, len(chainsToMonitor))
	for _, c := range chainsToMonitor {
		chainNames = append(chainNames, c.Name)
	}
	log.Printf("Discovered %d blockchains supporting stablecoins from /status: %s\n",
		len(chainsToMonitor), strings.Join(chainNames, ", "))

	// 3. Launch a monitoring goroutine for each discovered blockchain
	var wg sync.WaitGroup
	for _, chain := range chainsToMonitor {
		wg.Add(1)
		go monitorBlockchain(ctx, chain, apiKey, &wg)
	}

	// Wait for OS shutdown signal
	<-ctx.Done()
	log.Println("Cancellation signal received. Shutting down gracefully...")

	wg.Wait()
	log.Println("Stablecoin monitor stopped.")
}

// sleepContext pauses execution for d duration or until ctx is cancelled. Returns false if cancelled.
func sleepContext(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// getAPIKey resolves the API key from the -api-key flag, then the WHALE_ALERT_API_KEY
// environment variable, then the constant.
func getAPIKey(flagKey string) string {
	if flagKey != "" {
		return flagKey
	}
	if key := os.Getenv("WHALE_ALERT_API_KEY"); key != "" {
		return key
	}
	apiKey := whaleAlertApiKey
	if apiKey == "YOUR_API_KEY_HERE" {
		log.Fatal("Warning: WHALE_ALERT_API_KEY is not set. Please set the WHALE_ALERT_API_KEY environment variable, pass -api-key, or edit the constant in main.go.")
	}

	return apiKey
}

// fetchJSON executes an HTTP GET request, appends the API key, and decodes the JSON payload.
func fetchJSON(ctx context.Context, reqURL, apiKey string, target any) error {
	u, err := url.Parse(reqURL)
	if err != nil {
		return err
	}
	if apiKey != "" && u.Query().Get("api_key") == "" {
		q := u.Query()
		q.Set("api_key", apiKey)
		u.RawQuery = q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Error closing response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		bodyStr := strings.TrimSpace(string(bodyBytes))
		if bodyStr != "" {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, bodyStr)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(target)
}

// fetchAvailableBlockchains retrieves all active blockchains and supported symbols from /status.
func fetchAvailableBlockchains(ctx context.Context, apiKey string) ([]StatusChain, error) {
	var chains []StatusChain
	err := fetchJSON(ctx, fmt.Sprintf("%s/status", apiBaseURL), apiKey, &chains)
	if err != nil {
		return nil, err
	}
	return chains, nil
}

// getSupportedChains filters blockchains that support at least one tracked stablecoin.
func getSupportedChains(allChains []StatusChain) []StatusChain {
	var matched []StatusChain
	for _, chain := range allChains {
		for _, sym := range chain.Symbols {
			if _, ok := stablecoins[strings.ToLower(sym)]; ok {
				matched = append(matched, chain)
				break
			}
		}
	}
	return matched
}

// monitorBlockchain queries and polls a single blockchain for stablecoin events.
func monitorBlockchain(ctx context.Context, chain StatusChain, apiKey string, wg *sync.WaitGroup) {
	defer wg.Done()

	startHeight := chain.Height
	// If height was not in status or is 0, fetch from /{blockchain}/status
	for startHeight == 0 {
		latestHeight, err := getLatestHeight(ctx, chain.Name, apiKey)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[%s] Failed to fetch latest block height: %v. Retrying in 5s...", chain.Name, err)
			if !sleepContext(ctx, 5*time.Second) {
				return
			}
			continue
		}
		startHeight = latestHeight
	}

	log.Printf("[%s] Monitoring from block #%d...\n", formatBlockchain(chain.Name), startHeight)

	// Query initial transactions from the start block height
	nextURL := fmt.Sprintf("%s/%s/transactions?start_height=%d", apiBaseURL, chain.Name, startHeight)

	// Keep polling periodically using the "next" URL cursor
	for ctx.Err() == nil {
		resp, err := fetchTransactions(ctx, nextURL, apiKey)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[%s] Error fetching transactions: %v. Retrying in 5s...", chain.Name, err)
			if !sleepContext(ctx, 5*time.Second) {
				return
			}
			continue
		}

		for _, tx := range resp.Transactions {
			checkTransaction(chain.Name, tx)
		}

		if resp.Next != "" {
			nextURL = resp.Next
		}

		if !sleepContext(ctx, pollInterval) {
			return
		}
	}
}

// getLatestHeight fetches the newest block height from the status endpoint.
func getLatestHeight(ctx context.Context, chain, apiKey string) (uint64, error) {
	var status BlockchainStatus
	err := fetchJSON(ctx, fmt.Sprintf("%s/%s/status", apiBaseURL, chain), apiKey, &status)
	if err != nil {
		return 0, err
	}
	return status.EndHeight, nil
}

// fetchTransactions retrieves transactions from the given URL and parses the response.
func fetchTransactions(ctx context.Context, fetchURL, apiKey string) (*TransactionsResponse, error) {
	if !strings.HasPrefix(fetchURL, "http") {
		fetchURL = apiBaseURL + fetchURL
	}

	var data TransactionsResponse
	err := fetchJSON(ctx, fetchURL, apiKey, &data)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

// checkTransaction inspects a transaction's sub-transactions for supply and governance events:
// mints, burns, freezes, unfreezes, locks, and unlocks.
func checkTransaction(chain string, tx Transaction) {
	for _, sub := range tx.SubTransactions {
		symLower := strings.ToLower(sub.Symbol)

		// Step 1: Filter for tracked stablecoins
		if len(stablecoins) > 0 {
			if _, ok := stablecoins[symLower]; !ok {
				continue
			}
		}

		// Step 2: Filter for supported supply and governance event types
		switch sub.TransactionType {
		case "mint", "burn", "freeze", "unfreeze", "lock", "unlock":
			// Process supported supply and governance event types
		default:
			// Ignore standard transfers and unclassified movements
			continue
		}

		var amount float64

		// Step 3: Calculate token amount / address balance based on event type
		switch sub.TransactionType {
		case "mint":
			// Mint: creates new tokens at the output address(es)
			amount = sumAmounts(sub.Outputs)
		case "burn":
			// Burn: destroys tokens from the input address(es)
			amount = sumAmounts(sub.Inputs)
		case "lock", "unlock":
			// Lock / Unlock: tokens locked in or released from escrow
			amount = sumAmounts(sub.Inputs)
			if amount == 0 {
				amount = sumAmounts(sub.Outputs)
			}
		case "freeze":
			// Freeze: blacklists input address(es)
			amount = sumBalances(sub.Inputs)
		case "unfreeze":
			// Unfreeze: removes blacklist on output address(es)
			amount = sumBalances(sub.Outputs)
		}

		// If no positive amount could be calculated for transfer/mint/burn/lock/unlock, skip
		if sub.TransactionType != "freeze" && sub.TransactionType != "unfreeze" {
			if amount <= 0 {
				continue
			}
		}

		// Calculate USD valuation using the spot price provided by Whale Alert
		usdValue := amount * sub.Price

		// Filter out transactions below the configured minimum USD threshold to avoid noise
		if usdValue < minUSDThreshold {
			continue
		}

		// Format asset string and blockchain name
		assetStr := formatAsset(amount, sub.Symbol, usdValue)
		chainStr := formatBlockchain(chain)

		// Step 4: Resolve event-specific parties and labels
		var label1, party1, label2, party2 string
		amountLabel := "Asset"

		switch sub.TransactionType {
		case "mint":
			// Mints create new tokens to a recipient/treasury (destination only)
			label1, party1 = "To", getParty(sub.Outputs)
		case "burn":
			// Burns destroy tokens from a holder or issuer (source only)
			label1, party1 = "From", getParty(sub.Inputs)
		case "freeze":
			// Freezes blacklist a specific target address
			amountLabel = "Balance"
			label1, party1 = "Target", getParty(sub.Inputs)
		case "unfreeze":
			// Unfreezes restore transfer permissions to a target address
			amountLabel = "Balance"
			label1, party1 = "Target", getParty(sub.Outputs)
		case "lock":
			// Locks deposit tokens into a smart contract vault or bridge escrow
			label1, party1 = "From", getParty(sub.Inputs)
			label2, party2 = "Locked At", getParty(sub.Outputs)
		case "unlock":
			// Unlocks release escrowed tokens back into active circulation
			label1, party1 = "To", getParty(sub.Outputs)
		}

		// Step 5: Format and print event details to console (synchronized)
		printMu.Lock()
		fmt.Println()
		fmt.Printf("[STABLECOIN %s]\n", strings.ToUpper(sub.TransactionType))
		fmt.Printf("   %-12s %s\n", amountLabel+":", assetStr)
		fmt.Printf("   Blockchain:  %s\n", chainStr)
		if label1 != "" {
			fmt.Printf("   %-12s %s\n", label1+":", party1)
		}
		if label2 != "" {
			fmt.Printf("   %-12s %s\n", label2+":", party2)
		}
		if tx.Hash != "" {
			fmt.Printf("   Hash:        %s\n", tx.Hash)
		}
		printMu.Unlock()
	}
}

// sumAmounts sums parsed float amounts for a slice of accounts.
func sumAmounts(accounts []Account) float64 {
	var total float64
	for _, acc := range accounts {
		if amt, err := strconv.ParseFloat(acc.Amount, 64); err == nil && amt > 0 {
			total += amt
		}
	}
	return total
}

// sumBalances sums parsed float balances for a slice of accounts.
func sumBalances(accounts []Account) float64 {
	var total float64
	for _, acc := range accounts {
		if b, err := strconv.ParseFloat(acc.Balance, 64); err == nil && b > 0 {
			total += b
		}
	}
	return total
}

// formatAsset formats token amount, symbol, and USD valuation.
func formatAsset(amount float64, symbol string, valueUSD float64) string {
	symUpper := strings.ToUpper(symbol)
	if amount <= 0 {
		return fmt.Sprintf("0 %s", symUpper)
	}

	var amtStr string
	if amount == float64(int64(amount)) {
		amtStr = humanize.Comma(int64(amount))
	} else {
		amtStr = humanize.CommafWithDigits(amount, 2)
	}

	if valueUSD > 0 {
		return fmt.Sprintf("%s %s ($%s USD)", amtStr, symUpper, humanize.CommafWithDigits(valueUSD, 2))
	}
	return fmt.Sprintf("%s %s", amtStr, symUpper)
}

// formatBlockchain formats network names into standard title casing.
func formatBlockchain(chain string) string {
	if len(chain) == 0 {
		return "Unknown"
	}
	return strings.ToUpper(chain[:1]) + strings.ToLower(chain[1:])
}

// getParty resolves an account or list of accounts with formatted addresses and owner labels.
func getParty(accounts []Account) string {
	var parties []string
	seen := make(map[string]bool)

	for _, acc := range accounts {
		if acc.Address != "" && !isNullAddress(acc.Address) {
			if seen[acc.Address] {
				continue
			}
			seen[acc.Address] = true

			if acc.Owner != "" && !strings.EqualFold(acc.Owner, "unknown") {
				parties = append(parties, fmt.Sprintf("%s (%s)", formatShortAddress(acc.Address), acc.Owner))
			} else {
				parties = append(parties, formatShortAddress(acc.Address))
			}
		} else if acc.Owner != "" && !strings.EqualFold(acc.Owner, "unknown") && !seen[acc.Owner] {
			seen[acc.Owner] = true
			parties = append(parties, acc.Owner)
		}
	}

	if len(parties) > 0 {
		return strings.Join(parties, ", ")
	}
	return "Unknown"
}

// isNullAddress tests whether an address is a standard burn/null address.
func isNullAddress(addr string) bool {
	a := strings.ToLower(strings.TrimSpace(addr))
	return a == "" ||
		a == "null" ||
		a == "0x0" ||
		strings.HasPrefix(a, "0x0000000000000000000000000000000000000000") ||
		strings.HasPrefix(a, "0x000000000000000000000000000000000000dead") ||
		strings.HasPrefix(a, "11111111111111111111111111111111") ||
		a == "t9yd14nj9j7xab4dbgeix9h8unkkhxuwvb"
}

// formatShortAddress truncates long addresses for clean console output.
func formatShortAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	if len(addr) > 12 {
		return fmt.Sprintf("%s...%s", addr[:6], addr[len(addr)-4:])
	}
	return addr
}
