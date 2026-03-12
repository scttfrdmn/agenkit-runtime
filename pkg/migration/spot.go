// Package migration handles spot-eviction detection and session migration.
package migration

import (
	"context"
	"io"
	"log"
	"net/http"
	"time"
)

const (
	// imdsSpotURL is the EC2 instance metadata endpoint for spot interruption notices.
	imdsSpotURL = "http://169.254.169.254/latest/meta-data/spot/interruption-action"
	// imdsTokenURL is the EC2 IMDSv2 token endpoint.
	imdsTokenURL    = "http://169.254.169.254/latest/api/token"
	pollInterval    = 5 * time.Second
	imdsTimeout     = 2 * time.Second
	imdsTokenTTL    = "21600" // seconds
)

// SpotMonitor polls the EC2 instance metadata service for spot interruption notices.
//
// On a non-EC2 host (or when the metadata service returns 404) the monitor is a no-op.
// When an interruption notice is detected, OnInterruption is called exactly once with
// the estimated termination deadline (typically ~2 minutes from detection).
type SpotMonitor struct {
	client          *http.Client
	OnInterruption  func(deadline time.Time)
	imdsToken       string
	imdsTokenExpiry time.Time
}

// NewSpotMonitor creates a SpotMonitor with the given callback.
func NewSpotMonitor(onInterruption func(deadline time.Time)) *SpotMonitor {
	return &SpotMonitor{
		client: &http.Client{Timeout: imdsTimeout},
		OnInterruption: onInterruption,
	}
}

// Run starts the polling loop and blocks until ctx is cancelled.
// It is safe to call from a goroutine.
func (m *SpotMonitor) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if detected := m.poll(ctx); detected {
				// Spot warnings give ~2 minutes before termination.
				deadline := time.Now().Add(2 * time.Minute)
				log.Printf("INFO: SpotMonitor: interruption detected; deadline %s", deadline.Format(time.RFC3339))
				if m.OnInterruption != nil {
					m.OnInterruption(deadline)
				}
				return // stop polling once the interruption is confirmed
			}
		}
	}
}

// poll checks the IMDS endpoint once.
// Returns true if a spot interruption notice was found.
func (m *SpotMonitor) poll(ctx context.Context) bool {
	token, err := m.getIMDSToken(ctx)
	if err != nil {
		return false // not on EC2 or IMDS unavailable
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imdsSpotURL, nil)
	if err != nil {
		return false
	}
	if token != "" {
		req.Header.Set("X-aws-ec2-metadata-token", token)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	// 404 means no interruption notice yet.
	if resp.StatusCode == http.StatusNotFound {
		return false
	}

	// Any 2xx response body contains the action ("terminate" or "hibernate").
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
		log.Printf("DEBUG: SpotMonitor: IMDS returned status %d, body: %s", resp.StatusCode, body)
		return true
	}

	return false
}

// getIMDSToken fetches (and caches) an IMDSv2 session token.
// Returns an empty string if IMDSv2 is unavailable (falls back to v1 or non-EC2).
func (m *SpotMonitor) getIMDSToken(ctx context.Context) (string, error) {
	if time.Now().Before(m.imdsTokenExpiry) {
		return m.imdsToken, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, imdsTokenURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", imdsTokenTTL)

	resp, err := m.client.Do(req)
	if err != nil {
		return "", err // not on EC2
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", nil // IMDSv1 or unavailable — continue without token
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}

	m.imdsToken = string(body)
	m.imdsTokenExpiry = time.Now().Add(6 * time.Hour)
	return m.imdsToken, nil
}
