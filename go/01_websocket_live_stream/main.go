package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/gorilla/websocket"
)

const (
	// Whale Alert API Key.
	// You can replace this value or set the WHALE_ALERT_API_KEY environment variable.
	whaleAlertApiKey = "YOUR_API_KEY_HERE"

	wsURL          = "wss://leviathan.whale-alert.io/ws"
	connectTimeout = 30 * time.Second

	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

var (
	// The alert subscription message.
	// EDIT THESE VALUES TO CUSTOMIZE YOUR STREAM FILTER
	subscription = AlertSubscriptionJSON{
		// Use "subscribe_alerts" for transaction alerts, or "subscribe_socials" for Whale Alert social media posts.
		Type: "subscribe_alerts",

		// Optional subscription identifier
		// ID: "my_subscription",

		// Filters only valid for "subscribe_alerts" type. Ignored for "subscribe_socials".
		// Blockchains to filter by (lowercase). Leave empty to subscribe to all supported blockchains.
		Blockchains: []string{
			//"bitcoin",
			//"ethereum",
			//"tron",
		},

		// Currency symbols to filter by (lowercase). Leave empty to subscribe to all symbols.
		// Symbols: []string{
		// 	"btc", "eth", "usdt", "sol",
		// },

		// Transaction types to filter by. Leave empty to subscribe to all types.
		// Supported types: "transfer", "mint", "burn", "freeze", "unfreeze", "lock", "unlock"
		Types: []string{
			//"transfer",
			//"mint",
			//"burn",
			//"freeze",
			//"unfreeze",
			//"lock",
			//"unlock",
		},

		// Minimum transaction value in USD to trigger an alert
		MinValueUSD: 100_000,
	}
)

func main() {
	apiKeyFlag := flag.String("api-key", "", "Whale Alert API key (defaults to WHALE_ALERT_API_KEY env var)")
	flag.Parse()

	apiKey := getAPIKey(*apiKeyFlag)

	client := NewWebSocketClient(apiKey, wsURL, subscription)

	// Listen for interrupt signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Start the WebSocket client loop
	client.Start()
	log.Println("Whale Alert streaming client started. Press Ctrl+C to exit.")

	// Block until an OS termination signal is received
	sig := <-sigCh
	log.Printf("Received signal %v. Shutting down gracefully...\n", sig)

	client.Close()
	log.Println("WebSocket client stopped.")
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

// WebSocketClient manages the WebSocket connection lifecycle and message streaming.
type WebSocketClient struct {
	apiKey       string
	wsURL        string
	subscription AlertSubscriptionJSON

	conn   *websocket.Conn
	mu     sync.Mutex
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// NewWebSocketClient initializes a new WebSocket client instance.
func NewWebSocketClient(apiKey, wsURL string, sub AlertSubscriptionJSON) *WebSocketClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &WebSocketClient{
		apiKey:       apiKey,
		wsURL:        wsURL,
		subscription: sub,
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start launches the background connection and event listening loop.
func (c *WebSocketClient) Start() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()

		for {
			select {
			case <-c.ctx.Done():
				return
			default:
			}

			// Connect to WebSocket server
			if err := c.connect(); err != nil {
				if c.ctx.Err() == nil {
					log.Printf("Error connecting to WebSocket: %v\n", err)
				}
				c.backoff()
				continue
			}

			// Send alert subscription filter
			if err := c.subscribe(); err != nil {
				if c.ctx.Err() == nil {
					log.Printf("Error sending subscription: %v\n", err)
				}
				c.closeConnection()
				c.backoff()
				continue
			}

			// Keep reading messages until disconnected or stopped
			if err := c.readLoop(); err != nil {
				if !errors.Is(err, context.Canceled) && c.ctx.Err() == nil {
					log.Printf("Connection error: %v\n", err)
				}
				c.closeConnection()
				c.backoff()
			}
		}
	}()
}

// Close gracefully closes the WebSocket connection and waits for the worker goroutine to exit.
func (c *WebSocketClient) Close() {
	c.cancel()
	c.closeConnection()
	c.wg.Wait()
}

// connect establishes the WebSocket connection.
func (c *WebSocketClient) connect() error {
	ctx, cancel := context.WithTimeout(c.ctx, connectTimeout)
	defer cancel()

	endpoint := fmt.Sprintf("%s?api_key=%s", c.wsURL, c.apiKey)
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	log.Println("Connected to Whale Alert WebSocket server.")
	return nil
}

// subscribe sends the JSON subscription configuration to the server.
func (c *WebSocketClient) subscribe() error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return errors.New("cannot subscribe: connection is nil")
	}

	_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
	return conn.WriteJSON(&c.subscription)
}

// closeConnection closes the underlying active WebSocket connection.
func (c *WebSocketClient) closeConnection() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		_ = c.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
		_ = c.conn.Close()
		c.conn = nil
	}
}

// backoff waits briefly before attempting to reconnect.
func (c *WebSocketClient) backoff() {
	select {
	case <-c.ctx.Done():
	case <-time.After(5 * time.Second):
	}
}

// readLoop reads incoming messages from the WebSocket server.
func (c *WebSocketClient) readLoop() error {
	type MsgHeader struct {
		Type string `json:"type"`
	}

	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return errors.New("connection is nil")
	}

	// Configure ping/pong keepalive and heartbeat deadlines
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	conn.SetPingHandler(func(appData string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(writeWait))
	})

	// Start ping ticker to keep connection alive through NATs/proxies
	pingTicker := time.NewTicker(pingPeriod)
	defer pingTicker.Stop()

	stopPing := make(chan struct{})
	defer close(stopPing)

	go func() {
		for {
			select {
			case <-pingTicker.C:
				c.mu.Lock()
				activeConn := c.conn
				c.mu.Unlock()
				if activeConn == nil {
					return
				}
				if err := activeConn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait)); err != nil {
					return
				}
			case <-stopPing:
				return
			case <-c.ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		default:
		}

		_, buf, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		// Reset read deadline on receiving any message
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))

		var header MsgHeader
		if err := json.Unmarshal(buf, &header); err != nil {
			log.Printf("Error unmarshalling message header: %v\n", err)
			continue
		}

		switch header.Type {
		case "subscribed_alerts":
			var sub AlertSubscriptionJSON
			if err := json.Unmarshal(buf, &sub); err != nil {
				log.Printf("Error parsing subscribed_alerts: %v\n", err)
				continue
			}

			blockchains := "all"
			if len(sub.Blockchains) > 0 {
				blockchains = strings.Join(sub.Blockchains, ", ")
			}
			symbols := "all"
			if len(sub.Symbols) > 0 {
				symbols = strings.Join(sub.Symbols, ", ")
			}
			types := "all"
			if len(sub.Types) > 0 {
				types = strings.Join(sub.Types, ", ")
			}

			fmt.Println("================================================================================")
			fmt.Printf("SUBSCRIPTION CONFIRMED\n")
			fmt.Printf("  Blockchains: %s\n", blockchains)
			fmt.Printf("  Symbols:     %s\n", symbols)
			fmt.Printf("  Tx Types:    %s\n", types)
			fmt.Printf("  Min USD:     $%s\n", humanize.CommafWithDigits(sub.MinValueUSD, 0))
			fmt.Println("================================================================================")

		case "subscribed_socials":
			fmt.Println("================================================================================")
			fmt.Println("SUBSCRIPTION CONFIRMED: Subscribed to Whale Alert Socials Stream")
			fmt.Println("================================================================================")

		case "alert":
			var alert AlertJSON
			if err := json.Unmarshal(buf, &alert); err != nil {
				log.Printf("Error parsing alert: %v\n", err)
				continue
			}
			c.logAlert(alert)

		case "socials":
			var socials SocialsJSON
			if err := json.Unmarshal(buf, &socials); err != nil {
				log.Printf("Error parsing socials: %v\n", err)
				continue
			}
			c.logSocials(socials)

		default:
			log.Printf("Received message type [%s]: %s\n", header.Type, string(buf))
		}
	}
}

// logAlert outputs structured alert information to the console.
func (c *WebSocketClient) logAlert(alert AlertJSON) {
	summary := createAlertText(alert)
	t := time.Unix(alert.Timestamp, 0).UTC()

	fmt.Println()
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf("[%s] %s\n", strings.ToUpper(alert.Blockchain), summary)
	fmt.Printf("   Transaction Type: %s\n", alert.TransactionType)

	switch alert.TransactionType {
	case "mint", "unlock":
		if alert.To != "" {
			fmt.Printf("   To:               %s\n", alert.To)
		}
	case "burn", "lock":
		if alert.From != "" {
			fmt.Printf("   From:             %s\n", alert.From)
		}
	case "freeze":
		target := alert.From
		if target == "" {
			target = alert.To
		}
		if target != "" {
			fmt.Printf("   Target:           %s\n", target)
		}
	case "unfreeze":
		target := alert.To
		if target == "" {
			target = alert.From
		}
		if target != "" {
			fmt.Printf("   Target:           %s\n", target)
		}
	default:
		if alert.From != "" {
			fmt.Printf("   From:             %s\n", alert.From)
		}
		if alert.To != "" {
			fmt.Printf("   To:               %s\n", alert.To)
		}
	}

	for _, amt := range alert.Amounts {
		fmt.Printf("   Amount:           %s %s ($%s USD)\n",
			humanize.CommafWithDigits(amt.Amount, 2),
			strings.ToUpper(amt.Symbol),
			humanize.CommafWithDigits(amt.ValueUSD, 2),
		)
	}

	if alert.Transaction.Hash != "" {
		fmt.Printf("   Hash:             %s\n", alert.Transaction.Hash)
	}
	if alert.Transaction.BlockHeight > 0 {
		fmt.Printf("   Block Height:     #%d\n", alert.Transaction.BlockHeight)
	}
	fmt.Printf("   Timestamp:        %s (UNIX: %d)\n", t.Format("2006-01-02 15:04:05 UTC"), alert.Timestamp)
	fmt.Println("--------------------------------------------------------------------------------")
}

// logSocials outputs a formatted social alert to the console.
func (c *WebSocketClient) logSocials(socials SocialsJSON) {
	t := time.Unix(socials.Timestamp, 0).UTC()

	fmt.Println()
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf("[SOCIAL POST] %s\n", socials.Text)
	if socials.Blockchain != "" {
		fmt.Printf("   Blockchain: %s\n", strings.ToUpper(socials.Blockchain))
	}
	if len(socials.URLs) > 0 {
		fmt.Printf("   URLs:       %s\n", strings.Join(socials.URLs, ", "))
	}
	fmt.Printf("   Timestamp:  %s\n", t.Format("2006-01-02 15:04:05 UTC"))
	fmt.Println("--------------------------------------------------------------------------------")
}

// createAlertText constructs a human-readable description for an alert.
func createAlertText(alert AlertJSON) string {
	const (
		amountText   = "%s %s ($%s USD)"
		transferText = "%s transferred from %s to %s"
		mintText     = "%s minted at %s"
		burnText     = "%s burned at %s"
		lockText     = "%s locked at %s"
		unlockText   = "%s unlocked at %s"
		freezeText   = "%s frozen at %s"
		unfreezeText = "%s unfrozen at %s"
	)

	var amountsSlice []string
	for _, a := range alert.Amounts {
		amountsSlice = append(amountsSlice, fmt.Sprintf(amountText,
			humanize.CommafWithDigits(a.Amount, 2),
			strings.ToUpper(a.Symbol),
			humanize.CommafWithDigits(a.ValueUSD, 0),
		))
	}
	amounts := strings.Join(amountsSlice, ", ")

	from := alert.From
	if from == "" {
		from = "Unknown"
	}
	to := alert.To
	if to == "" {
		to = "Unknown"
	}

	switch alert.TransactionType {
	case "transfer":
		return fmt.Sprintf(transferText, amounts, from, to)
	case "mint":
		return fmt.Sprintf(mintText, amounts, to)
	case "burn":
		return fmt.Sprintf(burnText, amounts, from)
	case "lock":
		return fmt.Sprintf(lockText, amounts, from)
	case "unlock":
		return fmt.Sprintf(unlockText, amounts, to)
	case "freeze":
		return fmt.Sprintf(freezeText, amounts, from)
	case "unfreeze":
		return fmt.Sprintf(unfreezeText, amounts, to)
	default:
		if alert.Text != "" {
			return alert.Text
		}
		return fmt.Sprintf("%s: %s", alert.TransactionType, amounts)
	}
}

// AlertSubscriptionJSON represents the JSON message sent to the WebSocket server to subscribe to alerts.
// It is also returned by the WebSocket server when the subscription is confirmed.
type AlertSubscriptionJSON struct {
	Type        string   `json:"type"`                    // "subscribe_alerts" or "subscribe_socials"
	ID          string   `json:"id,omitempty"`            // Optional client-defined subscription ID
	Blockchains []string `json:"blockchains,omitempty"`   // Filter by blockchains (lowercase)
	Symbols     []string `json:"symbols,omitempty"`       // Filter by currency symbols (lowercase)
	Types       []string `json:"tx_types,omitempty"`      // Filter by transaction types (transfer, mint, burn, freeze, unfreeze, lock, unlock)
	MinValueUSD float64  `json:"min_value_usd,omitempty"` // Minimum transaction value in USD
}

// SocialsJSON represents a social media alert message received from the WebSocket server.
type SocialsJSON struct {
	Type       string   `json:"type"`         // Message type ("socials")
	ID         string   `json:"id,omitempty"` // Channel / subscription ID
	Timestamp  int64    `json:"timestamp"`    // UNIX timestamp
	Blockchain string   `json:"blockchain"`   // Associated blockchain
	Text       string   `json:"text"`         // Formatted alert text
	URLs       []string `json:"urls"`         // Direct links to social media posts
}

// AlertJSON represents a transaction alert received from the WebSocket server.
type AlertJSON struct {
	Type            string          `json:"type"`             // Message type ("alert")
	ID              string          `json:"channel_id"`       // Subscription / channel ID
	Timestamp       int64           `json:"timestamp"`        // UNIX timestamp
	Blockchain      string          `json:"blockchain"`       // Blockchain name
	TransactionType string          `json:"transaction_type"` // Transaction type (transfer, mint, burn, etc.)
	From            string          `json:"from"`             // Sender entity or address
	To              string          `json:"to"`               // Receiver entity or address
	Amounts         []AmountJSON    `json:"amounts"`          // Transferred amounts and values
	Text            string          `json:"text"`             // Human-readable summary text
	Transaction     TransactionJSON `json:"transaction"`      // Full transaction details
}

// AmountJSON represents a token/coin transfer amount and its estimated USD value.
type AmountJSON struct {
	Symbol   string  `json:"symbol"`    // Currency symbol (e.g. eth, btc, usdt)
	Amount   float64 `json:"amount"`    // Token / coin amount
	ValueUSD float64 `json:"value_usd"` // USD value of amount at transaction time
}

// TransactionJSON represents the complete transaction data of an alert.
type TransactionJSON struct {
	BlockHeight     uint64           `json:"height"`                     // Block height
	IndexInBlock    int              `json:"index_in_block"`             // Index inside the block
	Timestamp       int64            `json:"timestamp"`                  // UNIX timestamp
	Hash            string           `json:"hash"`                       // Transaction hash
	Fee             string           `json:"fee,omitempty"`              // Transaction fee
	FeeSymbol       string           `json:"fee_symbol,omitempty"`       // Currency symbol of fee
	FeeSymbolPrice  float64          `json:"fee_symbol_price,omitempty"` // USD price per unit of fee token
	SubTransactions []SubTransaction `json:"sub_transactions"`           // Sub-transactions modifying address balances
}

// SubTransaction represents a sub-operation within a multi-transfer transaction.
type SubTransaction struct {
	Symbol          string    `json:"symbol"`                   // Currency symbol
	Price           float64   `json:"unit_price_usd,omitempty"` // USD price per unit
	TransactionType string    `json:"transaction_type"`         // Type of sub-transaction
	Inputs          []Address `json:"inputs"`                   // Senders (FROM)
	Outputs         []Address `json:"outputs"`                  // Receivers (TO)
}

// Address represents an address involved in a sub-transaction with attribution metadata.
type Address struct {
	Amount      string `json:"amount"`                 // Amount by which address balance was changed
	Address     string `json:"address,omitempty"`      // Blockchain address
	Balance     string `json:"balance,omitempty"`      // Address balance after transaction
	Locked      string `json:"locked,omitempty"`       // Locked balance
	IsFrozen    bool   `json:"is_frozen,omitempty"`    // Whether address is frozen
	Owner       string `json:"owner,omitempty"`        // Identified entity name (e.g. "binance", "coinbase")
	OwnerType   string `json:"owner_type,omitempty"`   // Entity type (e.g. "exchange")
	AddressType string `json:"address_type,omitempty"` // Address classification (e.g. "hot_wallet", "cold_wallet")
}
