package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// Client provides access to the Navaris sandbox control plane API.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithURL sets the API base URL.
func WithURL(url string) Option {
	return func(c *Client) {
		c.baseURL = url
	}
}

// WithToken sets the bearer token for authentication.
func WithToken(token string) Option {
	return func(c *Client) {
		c.token = token
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// NewClient creates a new Navaris API client. Options override environment
// variable defaults (NAVARIS_API_URL, NAVARIS_TOKEN).
func NewClient(opts ...Option) *Client {
	c := &Client{
		baseURL:    "http://localhost:8080",
		httpClient: http.DefaultClient,
	}

	// Apply environment variable defaults first
	if v := os.Getenv("NAVARIS_API_URL"); v != "" {
		c.baseURL = v
	}
	if v := os.Getenv("NAVARIS_TOKEN"); v != "" {
		c.token = v
	}

	// Apply explicit options (override env vars)
	for _, o := range opts {
		o(c)
	}

	return c
}

// APIError represents a non-2xx response from the API.
type APIError struct {
	StatusCode int
	Code       int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("api error %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("api error: HTTP %d", e.StatusCode)
}

type apiErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) doRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		apiErr := &APIError{StatusCode: resp.StatusCode}
		data, _ := io.ReadAll(resp.Body)
		var eb apiErrorBody
		if json.Unmarshal(data, &eb) == nil && eb.Error.Code != 0 {
			apiErr.Code = eb.Error.Code
			apiErr.Message = eb.Error.Message
		} else if len(data) > 0 {
			apiErr.Message = string(data)
		}
		return nil, apiErr
	}

	return resp, nil
}

// get performs a GET request and decodes the response into v.
func (c *Client) get(ctx context.Context, path string, v any) error {
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// getList performs a GET request for a list endpoint and decodes the "data" array.
func getList[T any](c *Client, ctx context.Context, path string) ([]T, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var lr listResponse[T]
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}
	return lr.Data, nil
}

// post performs a POST request and decodes the response into v.
func (c *Client) post(ctx context.Context, path string, body, v any) error {
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if v != nil {
		return json.NewDecoder(resp.Body).Decode(v)
	}
	return nil
}

// put performs a PUT request and decodes the response into v.
func (c *Client) put(ctx context.Context, path string, body, v any) error {
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if v != nil {
		return json.NewDecoder(resp.Body).Decode(v)
	}
	return nil
}

// del performs a DELETE request. It expects a 204 No Content response.
func (c *Client) del(ctx context.Context, path string) error {
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// delWithResponse performs a DELETE request and decodes the response into v.
func (c *Client) delWithResponse(ctx context.Context, path string, v any) error {
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if v != nil {
		return json.NewDecoder(resp.Body).Decode(v)
	}
	return nil
}

// Health returns the health status of the backend provider.
func (c *Client) Health(ctx context.Context) (*ProviderHealth, error) {
	var h ProviderHealth
	if err := c.get(ctx, "/v1/health", &h); err != nil {
		return nil, err
	}
	return &h, nil
}
