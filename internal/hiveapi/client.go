// Package hiveapi consulta la API pública de un nodo Hive (condenser_api)
// para obtener las claves públicas configuradas en una cuenta.
package hiveapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// DefaultNode es el nodo público que se usa si no se configura otro.
const DefaultNode = "https://api.hive.blog"

// cacheTTL es cuánto tiempo se recuerda la clave posting de una cuenta antes
// de volver a consultar el nodo Hive. Evita golpear la API en cada evento.
const cacheTTL = 5 * time.Minute

// Client consulta un nodo Hive vía JSON-RPC (condenser_api.get_accounts) y
// cachea en memoria las claves "posting" resueltas.
type Client struct {
	Node       string
	HTTPClient *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	postingKey string
	expiresAt  time.Time
}

// NewClient crea un cliente para el nodo indicado. Si node está vacío, usa
// DefaultNode.
func NewClient(node string) *Client {
	if node == "" {
		node = DefaultNode
	}
	return &Client{
		Node:       node,
		HTTPClient: &http.Client{Timeout: 8 * time.Second},
		cache:      make(map[string]cacheEntry),
	}
}

type rpcRequest struct {
	Jsonrpc string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

type keyAuth struct {
	WeightThreshold int      `json:"weight_threshold"`
	KeyAuths        [][2]any `json:"key_auths"`
}

type hiveAccount struct {
	Name    string  `json:"name"`
	Posting keyAuth `json:"posting"`
}

type rpcResponse struct {
	Result []hiveAccount `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// GetPostingPublicKey devuelve la clave pública "posting" (formato "STM...")
// configurada para account, consultando el nodo Hive (con caché de 5 minutos).
//
// Limitación conocida: solo se soporta el caso común de una única clave
// (key_auths[0]). Cuentas con autoridad "posting" delegada a otra cuenta
// (account_auths) o con múltiples claves (multisig real) no están soportadas
// todavía; en ese caso se usa igualmente la primera clave de key_auths.
func (c *Client) GetPostingPublicKey(ctx context.Context, account string) (string, error) {
	if account == "" {
		return "", fmt.Errorf("nombre de cuenta hive vacío")
	}

	if key, ok := c.fromCache(account); ok {
		return key, nil
	}

	key, err := c.fetchPostingKey(ctx, account)
	if err != nil {
		return "", err
	}

	c.storeInCache(account, key)
	return key, nil
}

func (c *Client) fromCache(account string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.cache[account]
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.postingKey, true
}

func (c *Client) storeInCache(account, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[account] = cacheEntry{
		postingKey: key,
		expiresAt:  time.Now().Add(cacheTTL),
	}
}

func (c *Client) fetchPostingKey(ctx context.Context, account string) (string, error) {
	reqBody := rpcRequest{
		Jsonrpc: "2.0",
		Method:  "condenser_api.get_accounts",
		Params:  []interface{}{[]string{account}},
		ID:      1,
	}

	buf, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Node, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("no se pudo contactar el nodo hive %q: %w", c.Node, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("no se pudo leer la respuesta del nodo hive: %w", err)
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return "", fmt.Errorf("respuesta inesperada del nodo hive: %w", err)
	}
	if rpcResp.Error != nil {
		return "", fmt.Errorf("el nodo hive devolvió un error: %s", rpcResp.Error.Message)
	}
	if len(rpcResp.Result) == 0 {
		return "", fmt.Errorf("la cuenta hive %q no existe", account)
	}

	ka := rpcResp.Result[0].Posting
	if len(ka.KeyAuths) == 0 {
		return "", fmt.Errorf("la cuenta hive %q no tiene una clave posting configurada", account)
	}

	key, ok := ka.KeyAuths[0][0].(string)
	if !ok || key == "" {
		return "", fmt.Errorf("formato de clave posting inesperado para la cuenta %q", account)
	}

	return key, nil
}
