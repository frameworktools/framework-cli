package cloud

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// DeviceCode is the response of POST /api/v1/cli/device.
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURL         string `json:"verification_url"`
	VerificationURLComplete string `json:"verification_url_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
}

// ErrDeviceUnsupported means the server has no browser sign-in.
var ErrDeviceUnsupported = errors.New("this server does not offer browser sign-in")

// ErrDeviceDenied means the user declined the request in the browser.
var ErrDeviceDenied = errors.New("the sign-in request was denied in the browser")

// ErrDeviceExpired means the code expired or was already used.
var ErrDeviceExpired = errors.New("the sign-in code expired; run fw login again")

// StartDevice begins browser sign-in.
func (c *Client) StartDevice(ctx context.Context, clientName string) (DeviceCode, error) {
	var code DeviceCode
	err := c.Do(ctx, http.MethodPost, "/api/v1/cli/device", map[string]string{"client_name": clientName}, &code)
	if s := StatusOf(err); s == http.StatusNotFound || s == http.StatusMethodNotAllowed {
		return code, ErrDeviceUnsupported
	}
	if code.Interval < 1 {
		code.Interval = 5
	}
	return code, err
}

// PollDevice waits until the user approves the code and returns the token.
// sleep is replaced in tests.
func (c *Client) PollDevice(ctx context.Context, code DeviceCode, sleep func(context.Context, time.Duration) error) (string, error) {
	interval := time.Duration(code.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(code.ExpiresIn) * time.Second)
	for {
		if err := sleep(ctx, interval); err != nil {
			return "", err
		}
		var resp struct {
			Token string `json:"token"`
		}
		err := c.Do(ctx, http.MethodPost, "/api/v1/cli/device/token", map[string]string{"device_code": code.DeviceCode}, &resp)
		switch StatusOf(err) {
		case 0:
			if err != nil {
				return "", err
			}
			return resp.Token, nil
		case http.StatusPreconditionRequired: // authorization_pending
		case http.StatusTooManyRequests: // slow_down
			interval += 5 * time.Second
		case http.StatusForbidden:
			return "", ErrDeviceDenied
		case http.StatusGone:
			return "", ErrDeviceExpired
		default:
			return "", err
		}
		if code.ExpiresIn > 0 && time.Now().After(deadline) {
			return "", ErrDeviceExpired
		}
	}
}

// Sleep waits for d or until ctx ends.
func Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
