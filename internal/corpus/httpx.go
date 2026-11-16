package corpus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	defaultRetries    = 4
	defaultBackoff    = time.Second
	maxRetryAfter     = time.Minute
	defaultHTTPClient = 10 * time.Minute
)

// statusError is a non-200 HTTP response.
type statusError struct {
	URL        string
	Code       int
	Status     string
	RetryAfter time.Duration
}

func (e *statusError) Error() string { return fmt.Sprintf("GET %s: %s", e.URL, e.Status) }

// transportError is a failure to send a request or read its body.
type transportError struct{ err error }

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

// retryDelay reports whether err is worth retrying and any delay the server
// asked for. Only rate limiting, server errors and transport failures are.
func retryDelay(err error) (time.Duration, bool) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0, false
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.RetryAfter, se.Code == http.StatusTooManyRequests || se.Code >= 500
	}
	var te *transportError
	return 0, errors.As(err, &te)
}

// withRetry runs fn, retrying retryable failures up to retries more times with
// exponential backoff.
func withRetry(ctx context.Context, retries int, backoff time.Duration, fn func() error) error {
	for attempt := 0; ; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		wait, ok := retryDelay(err)
		if !ok || attempt >= retries {
			return err
		}
		if wait <= 0 {
			wait = backoff << attempt
		}
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// get performs one GET and returns the response only when its status is 200.
func get(ctx context.Context, client *http.Client, userAgent, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, &transportError{err}
	}
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		return nil, &statusError{
			URL:        url,
			Code:       resp.StatusCode,
			Status:     resp.Status,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	return resp, nil
}

// parseRetryAfter reads the delay-seconds form of Retry-After, capped so a
// misbehaving server cannot stall the tool.
func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		return 0
	}
	return min(time.Duration(secs)*time.Second, maxRetryAfter)
}

func defaultClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: defaultHTTPClient}
}
