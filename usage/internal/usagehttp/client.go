// Package usagehttp bounds authenticated provider usage reads.
package usagehttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxBodyBytes bounds a usage report; a model list is read within a bound of
// its own.
const maxBodyBytes = 64 * 1024

// Response retains only the fields a provider reader consumes.
type Response struct {
	Body       []byte
	StatusCode int
	ObservedAt time.Time
	RetryAt    time.Time
}

// Get makes one request without redirects, cookies, or inference.
func Get(ctx context.Context, transport http.RoundTripper, endpoint, token string, headers http.Header) (Response, error) {
	return GetWithin(ctx, transport, endpoint, token, headers, maxBodyBytes)
}

// GetWithin reads endpoint with the bearer, refusing a body larger than
// maxBytes.
func GetWithin(ctx context.Context, transport http.RoundTripper, endpoint, token string, headers http.Header, maxBytes int64) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}

	if strings.TrimSpace(token) == "" {
		return Response{StatusCode: http.StatusUnauthorized, ObservedAt: time.Now().UTC()}, nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return Response{}, errors.New("invalid provider usage endpoint")
	}

	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "acp-go-core/usage")
	maps.Copy(request.Header, headers)

	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}

		return Response{}, errors.New("provider usage request failed")
	}
	defer response.Body.Close()

	observedAt := time.Now().UTC()

	result := Response{StatusCode: response.StatusCode, ObservedAt: observedAt, RetryAt: retryAt(response.Header.Get("Retry-After"), observedAt)}
	if response.StatusCode != http.StatusOK {
		return result, nil
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))

	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}

	if err != nil || int64(len(body)) > maxBytes {
		return Response{}, errors.New("provider usage response exceeds its read bound or is incomplete")
	}

	result.Body = body

	return result, nil
}

// Decode rejects incomplete JSON without returning response bodies in errors.
func (r Response) Decode(value any) error {
	if err := json.Unmarshal(r.Body, value); err != nil {
		return errors.New("invalid provider usage response")
	}

	return nil
}

func retryAt(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
		return now.Add(time.Duration(seconds) * time.Second).Truncate(time.Second).Add(time.Second)
	}

	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return when.UTC()
	}

	return time.Time{}
}
