// Package usagehttp bounds authenticated provider usage reads.
package usagehttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxBodyBytes = 64 * 1024

// Response retains only the fields a provider reader consumes.
type Response struct {
	Body       []byte
	StatusCode int
	ObservedAt time.Time
}

// Get makes one request without redirects, cookies, or inference.
func Get(ctx context.Context, transport http.RoundTripper, endpoint, token string) (Response, error) {
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

	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))

	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}

	if err != nil || len(body) > maxBodyBytes {
		return Response{}, errors.New("provider usage response exceeds its read bound or is incomplete")
	}

	return Response{Body: body, StatusCode: response.StatusCode, ObservedAt: time.Now().UTC()}, nil
}

// Decode rejects incomplete JSON without returning response bodies in errors.
func (r Response) Decode(value any) error {
	if err := json.Unmarshal(r.Body, value); err != nil {
		return errors.New("invalid provider usage response")
	}

	return nil
}
