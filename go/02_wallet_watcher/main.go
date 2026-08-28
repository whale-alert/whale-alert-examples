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

	apiBaseURL   = "https://leviathan.whale-alert.io"
	blockchain   = "ethereum"
	pollInterval = 3 * time.Second
	httpTimeout  = 15 * time.Second

	// Address to monitor and its friendly display label
	watchedAddress = "0x28c6c06298d514db089934071355e5743bf21d60"
	watchedLabel   = "Binance 14"
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
	TransactionType string    `json:"transaction_type,omitempty"`
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

func main() {
	apiKeyFlag := flag.String("api-key", "", "Whale Alert API key (defaults to WHALE_ALERT_API_KEY env var)")
	flag.Parse()

	apiKey := getAPIKey(*apiKeyFlag)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Listen for interrupt signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("Received signal %v. Shutting down gracefully...\n", sig)
		cancel()
	}()

	// 1. Get the latest block height for the chain
	latestHeight, err := getLatestHeight(ctx, blockchain, apiKey)
	if err != nil {
		log.Fatalf("Failed to fetch latest block height for %s: %v", blockchain, err)
	}
	log.Printf("Monitoring %s address %s (%s) from block #%d...\n", blockchain, watchedAddress, watchedLabel, latestHeight)

	// 2. Query initial transactions filtered by the target address starting from the latest block height
	nextURL := fmt.Sprintf("%s/%s/transactions?start_height=%d&address=%s", apiBaseURL, blockchain, latestHeight, url.QueryEscape(watchedAddress))

	// 3. Keep polling periodically using the "next" URL cursor
	for ctx.Err() == nil {
		resp, err := fetchTransactions(ctx, nextURL, apiKey)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Printf("Error fetching transactions: %v. Retrying in 5s...", err)
			if !sleepContext(ctx, 5*time.Second) {
				break
			}
			continue
		}

		for _, tx := range resp.Transactions {
			checkTransaction(tx)
		}

		if resp.Next != "" {
			nextURL = resp.Next
		}

		if !sleepContext(ctx, pollInterval) {
			break
		}
	}

	log.Println("Wallet watcher stopped.")
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

// getLatestHeight fetches the newest block height from the status endpoint.
func getLatestHeight(ctx context.Context, chain, apiKey string) (uint64, error) {
	var status BlockchainStatus
	reqURL := fmt.Sprintf("%s/%s/status", apiBaseURL, chain)
	err := fetchJSON(ctx, reqURL, apiKey, &status)
	if err != nil {
		return 0, err
	}
	return status.EndHeight, nil
}

// fetchTransactions retrieves transactions from the given URL and parses the response.
func fetchTransactions(ctx context.Context, fetchURL, apiKey string) (*TransactionsResponse, error) {
	if !strings.HasPrefix(fetchURL, "http://") && !strings.HasPrefix(fetchURL, "https://") {
		if !strings.HasPrefix(fetchURL, "/") {
			fetchURL = "/" + fetchURL
		}
		fetchURL = apiBaseURL + fetchURL
	}
	var data TransactionsResponse
	err := fetchJSON(ctx, fetchURL, apiKey, &data)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

// checkTransaction inspects a transaction's sub-transactions for activity involving the watched address.
func checkTransaction(tx Transaction) {
	// A blockchain transaction can contain multiple sub-transactions (e.g., token swaps, batch transfers).
	// We evaluate each sub-transaction independently because each has its own asset symbol, unit price, inputs, and outputs.
	for _, sub := range tx.SubTransactions {
		watchedInputs := filterAccounts(sub.Inputs, watchedAddress, true)
		watchedOutputs := filterAccounts(sub.Outputs, watchedAddress, true)

		// Skip if the watched address had no role in this specific sub-transaction
		if len(watchedInputs) == 0 && len(watchedOutputs) == 0 {
			continue
		}

		inAmt := sumAmounts(watchedInputs)
		outAmt := sumAmounts(watchedOutputs)

		// The address is listed but nothing moved. Freezes and unfreezes look like this:
		// they carry their magnitude in `balance`, not `amount`. Reporting them here would
		// print "0.00" with no information; example 03 covers those events properly.
		if inAmt == 0 && outAmt == 0 {
			continue
		}

		otherInputs := filterAccounts(sub.Inputs, watchedAddress, false)
		otherOutputs := filterAccounts(sub.Outputs, watchedAddress, false)

		var direction string
		var amount float64
		var fromAccounts, toAccounts []Account

		switch {
		case inAmt > outAmt:
			// skip fee payments
			if outAmt == 0 && sub.TransactionType == "transfer" {
				continue
			}

			// Spent more than received back (UTXO change) -> Net OUTGOING
			direction = "OUTGOING"
			amount = inAmt - outAmt
			fromAccounts = watchedInputs
			toAccounts = otherOutputs

			// No external recipients. A sweep back to the watched address itself, minus the
			// network fee, is a self-transfer. Burns and locks have no outputs at all, so
			// nothing came back and the movement stays OUTGOING.
			if len(toAccounts) == 0 && len(watchedOutputs) > 0 {
				direction = "SELF-TRANSFER"
				amount = outAmt
				if amount == 0 {
					amount = inAmt
				}
				toAccounts = watchedOutputs
			}

		case outAmt > inAmt:
			// Received more than contributed -> Net INCOMING
			direction = "INCOMING"
			amount = outAmt - inAmt
			toAccounts = watchedOutputs
			fromAccounts = otherInputs

			if len(fromAccounts) == 0 {
				fromAccounts = sub.Inputs
			}

		default:
			// In == Out -> Pure 1:1 self-transfer
			direction = "SELF-TRANSFER"
			amount = inAmt
			fromAccounts = watchedInputs
			toAccounts = watchedOutputs
		}

		// Asset Valuation & Symbol normalization
		usdValue := amount * sub.Price
		symUpper := strings.ToUpper(strings.TrimSpace(sub.Symbol))
		if symUpper == "" {
			symUpper = "N/A"
		}

		// Format and Print the Match Alert to Console
		fmt.Println()
		fmt.Printf("[WATCHLIST MATCH] %s Wallet Activity\n", watchedLabel)
		fmt.Printf("   Direction:   %s\n", direction)
		if sub.TransactionType != "" && sub.TransactionType != "transfer" {
			fmt.Printf("   Type:        %s\n", strings.ToUpper(sub.TransactionType))
		}

		// Print asset amount and optional USD valuation
		if usdValue > 0 {
			fmt.Printf("   Asset:       %s %s ($%s USD)\n",
				formatAmount(amount),
				symUpper,
				humanize.CommafWithDigits(usdValue, 2),
			)
		} else {
			fmt.Printf("   Asset:       %s %s\n",
				formatAmount(amount),
				symUpper,
			)
		}

		// Print unit price if available
		if sub.Price > 0 {
			fmt.Printf("   Price:       $%s USD / %s\n",
				formatAmount(sub.Price),
				symUpper,
			)
		}

		// Format sender and receiver parties with labels/attributions
		fmt.Printf("   From:        %s\n", formatParties(fromAccounts))
		fmt.Printf("   To:          %s\n", formatParties(toAccounts))

		// Print transaction hash, block height, and timestamp
		if tx.Hash != "" {
			fmt.Printf("   Tx Hash:     %s\n", tx.Hash)
		}
		if tx.BlockHeight > 0 {
			fmt.Printf("   Block:       #%d\n", tx.BlockHeight)
		}
		if tx.Timestamp > 0 {
			t := time.Unix(tx.Timestamp, 0).UTC()
			fmt.Printf("   Timestamp:   %s\n", t.Format("2006-01-02 15:04:05 UTC"))
		}
		fmt.Println("--------------------------------------------------------------------------------")
	}
}

// filterAccounts returns accounts matching (match=true) or excluding (match=false) the target address.
func filterAccounts(accounts []Account, targetAddr string, match bool) []Account {
	var res []Account
	for _, acc := range accounts {
		// Compare case-insensitively: EVM addresses are commonly returned in
		// EIP-55 checksummed (mixed-case) form.
		if strings.EqualFold(acc.Address, targetAddr) == match {
			res = append(res, acc)
		}
	}
	return res
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

// formatAmount formats numbers with suitable precision (comma-separated for >= 1, up to 8 decimals for < 1).
func formatAmount(amount float64) string {
	if amount <= 0 {
		return "0.00"
	}
	if amount >= 1.0 {
		return humanize.CommafWithDigits(amount, 2)
	}
	str := strconv.FormatFloat(amount, 'f', 8, 64)
	return strings.TrimRight(strings.TrimRight(str, "0"), ".")
}

// shortenAddress truncates long addresses for clean console output.
func shortenAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	if len(addr) > 14 {
		return fmt.Sprintf("%s...%s", addr[:6], addr[len(addr)-4:])
	}
	return addr
}

// formatParties formats a list of accounts with their labels/owners, deduplicating identical entries.
func formatParties(accounts []Account) string {
	if len(accounts) == 0 {
		return "Unknown"
	}
	var parts []string
	seen := make(map[string]bool)
	for _, acc := range accounts {
		formatted := formatParty(acc)
		if !seen[formatted] {
			seen[formatted] = true
			parts = append(parts, formatted)
		}
	}
	if len(parts) == 0 {
		return "Unknown"
	}
	return strings.Join(parts, ", ")
}

// formatParty formats an individual account with its label or owner.
func formatParty(acc Account) string {
	if strings.EqualFold(acc.Address, watchedAddress) {
		return fmt.Sprintf("%s (%s)", acc.Address, watchedLabel)
	}
	shortAddr := shortenAddress(acc.Address)
	if acc.Owner != "" && !strings.EqualFold(acc.Owner, "unknown") {
		ownerDesc := acc.Owner
		if acc.OwnerType != "" && !strings.EqualFold(acc.OwnerType, "unknown") {
			ownerType := acc.OwnerType
			if len(ownerType) > 0 {
				ownerType = strings.ToUpper(ownerType[:1]) + strings.ToLower(ownerType[1:])
			}
			ownerDesc = fmt.Sprintf("%s [%s]", acc.Owner, ownerType)
		}
		if shortAddr != "" {
			return fmt.Sprintf("%s (%s)", shortAddr, ownerDesc)
		}
		return ownerDesc
	}
	if shortAddr != "" {
		return fmt.Sprintf("%s (Unknown)", shortAddr)
	}
	return "Unknown"
}
