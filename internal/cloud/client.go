package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// UserAgent is sent with every request. The CLI sets the version.
var UserAgent = "framework-cli/dev"

// Client calls one Framework Cloud server.
type Client struct {
	URL   string // normalized server URL
	Token string // bearer token, or "" for unauthenticated calls
	HTTP  *http.Client
}

// NewClient returns a client with a 30-second timeout.
func NewClient(url, token string) *Client {
	return &Client{URL: url, Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// APIError is a non-2xx response. Message comes from the server's JSON body.
type APIError struct {
	Status  int
	Message string
	Code    string // the "error" field, used by the device flow
	// Problems lists Laravel's validation errors, field by field.
	Problems []string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		msg := fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
		if len(e.Problems) > 0 {
			msg += ": " + strings.Join(e.Problems, "; ")
		}
		return msg
	}
	if e.Code != "" {
		return fmt.Sprintf("%s (HTTP %d)", e.Code, e.Status)
	}
	return fmt.Sprintf("request failed (HTTP %d)", e.Status)
}

// UnreachableError is a request that never got a response: a network
// error or a timeout.
type UnreachableError struct {
	Host string
	Err  error
}

func (e *UnreachableError) Error() string { return fmt.Sprintf("cannot reach %s: %v", e.Host, e.Err) }

func (e *UnreachableError) Unwrap() error { return e.Err }

// IsOffline reports whether err means the server is unavailable: it could
// not be reached, or it answered with a 5xx. 401 and 403 are not offline.
func IsOffline(err error) bool {
	var unreachable *UnreachableError
	return errors.As(err, &unreachable) || StatusOf(err) >= 500
}

// StatusOf returns the HTTP status of an APIError, or 0.
func StatusOf(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status
	}
	return 0
}

// Do sends a request with an optional JSON body and decodes a JSON response
// into out when out is not nil.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, out)
}

// do adds the common headers, sends req, and decodes a JSON response into
// out when out is not nil.
func (c *Client) do(req *http.Request, out any) error {
	_, err := c.doHeader(req, out)
	return err
}

// doHeader is do that also returns the response headers.
func (c *Client) doHeader(req *http.Request, out any) (http.Header, error) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, &UnreachableError{Host: Host(c.URL), Err: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, &UnreachableError{Host: Host(c.URL), Err: err}
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &APIError{Status: resp.StatusCode}
		var payload struct {
			Message string              `json:"message"`
			Error   string              `json:"error"`
			Errors  map[string][]string `json:"errors"`
		}
		if json.Unmarshal(data, &payload) == nil {
			apiErr.Message, apiErr.Code = payload.Message, payload.Error
			fields := make([]string, 0, len(payload.Errors))
			for field := range payload.Errors {
				fields = append(fields, field)
			}
			sort.Strings(fields)
			for _, field := range fields {
				for _, problem := range payload.Errors[field] {
					if problem != payload.Message {
						apiErr.Problems = append(apiErr.Problems, problem)
					}
				}
			}
		}
		return nil, apiErr
	}
	if out == nil {
		return resp.Header, nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return nil, fmt.Errorf("unexpected response from %s: %w", Host(c.URL), err)
	}
	return resp.Header, nil
}

// Me is the response of GET /api/v1/me.
type Me struct {
	Kind    string `json:"kind"` // "user" or "project"
	Name    string `json:"name"`
	Email   string `json:"email"`
	Project string `json:"project"`
}

// Who describes the token's owner for messages.
func (m Me) Who() string {
	switch {
	case m.Email != "":
		return m.Email
	case m.Project != "":
		return "project " + m.Project
	default:
		return "an authenticated token"
	}
}

// Me identifies the token.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var me Me
	err := c.Do(ctx, http.MethodGet, "/api/v1/me", nil, &me)
	return me, err
}

// Project is one entry of GET /api/v1/projects.
type Project struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	ProjectType string `json:"project_type"`
	Language    string `json:"language"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

// Projects lists the signed-in user's projects.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var resp struct {
		Projects []Project `json:"projects"`
	}
	err := c.Do(ctx, http.MethodGet, "/api/v1/projects", nil, &resp)
	return resp.Projects, err
}

// ProjectLayers fetches the signed-in user's project by slug as a layers
// envelope (raw JSON, validated by the caller).
func (c *Client) ProjectLayers(ctx context.Context, slug string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.Do(ctx, http.MethodGet, "/api/v2/projects/"+url.PathEscape(slug)+"/layers", nil, &raw)
	return raw, err
}

// PullLayers fetches the layers of the project a project token belongs to.
func (c *Client) PullLayers(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.Do(ctx, http.MethodGet, "/api/v2/projects/pull", nil, &raw)
	return raw, err
}

// PullToken is a new project token and the server that issued it.
type PullToken struct {
	Token string `json:"token"`
	URL   string `json:"url"`
}

// NewPullToken mints a project token for the user's project. Every call
// creates a new token, which the user can revoke on the project page.
func (c *Client) NewPullToken(ctx context.Context, slug, name string) (PullToken, error) {
	var token PullToken
	err := c.Do(ctx, http.MethodPost, "/api/v2/projects/"+url.PathEscape(slug)+"/pull-tokens", map[string]string{"name": name}, &token)
	return token, err
}
