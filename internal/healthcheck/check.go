package healthcheck

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
)

// Check probes only the service/database health endpoint, never the board.
func Check(ctx context.Context, address, token string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("invalid health address")
	}
	if host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/api/health", nil)
	if err != nil {
		return errors.New("health request unavailable")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("health request failed")
	}
	defer resp.Body.Close()
	p, err := io.ReadAll(io.LimitReader(resp.Body, 1025))
	var result struct {
		Status string `json:"status"`
	}
	if err != nil || resp.StatusCode != 200 || len(p) > 1024 || json.Unmarshal(p, &result) != nil || result.Status != "ok" {
		return errors.New("service not healthy")
	}
	return nil
}
