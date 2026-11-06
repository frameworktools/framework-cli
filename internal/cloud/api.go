package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"
)

// ProjectDetail is the response of GET /api/v1/projects/{slug}.
type ProjectDetail struct {
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Language    string     `json:"language"`
	Type        string     `json:"type"`
	Blueprint   *Blueprint `json:"blueprint"`
	GitHub      *GitHub    `json:"github"`
	CreatedAt   string     `json:"created_at"`
	UpdatedAt   string     `json:"updated_at"`
	// SpecReceivedAt is when fw project push-spec last sent the spec.
	SpecReceivedAt *string `json:"spec_received_at"`
}

// GitHub is a project's linked repository.
type GitHub struct {
	Repository     string `json:"repository"`
	URL            string `json:"url"`
	Private        bool   `json:"private"`
	ScaffoldStatus string `json:"scaffold_status"`
	// ScaffoldError says why scaffolding failed.
	ScaffoldError string `json:"scaffold_error,omitempty"`
	// PagesURL is the repository's GitHub Pages address, or nil while Pages
	// is off.
	PagesURL *string `json:"pages_url"`
}

// Project fetches one of the signed-in user's projects.
func (c *Client) Project(ctx context.Context, slug string) (ProjectDetail, error) {
	var p ProjectDetail
	err := c.Do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(slug), nil, &p)
	return p, err
}

// Blueprint is one entry of GET /api/v1/blueprints. A project's blueprint
// carries only Key and Version.
type Blueprint struct {
	Key         string `json:"key"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Language    string `json:"language,omitempty"`
	Type        string `json:"type,omitempty"`
	Version     int    `json:"version"`
	// Languages the blueprint supports; empty means all of them.
	Languages  []string `json:"languages,omitempty"`
	Visibility string   `json:"visibility,omitempty"`
	System     bool     `json:"system,omitempty"`
	// Facets are listed by servers from before structures and tools. Newer
	// ones list the blueprint's pieces instead, and keep facets for fw 0.3.
	Facets      []string `json:"facets,omitempty"`
	Structure   string   `json:"structure,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	PackageSets []string `json:"package_sets,omitempty"`
	SkillSets   []string `json:"skill_sets,omitempty"`
}

// Blueprints lists the blueprints the signed-in user can use.
func (c *Client) Blueprints(ctx context.Context) ([]Blueprint, error) {
	var resp struct {
		Blueprints []Blueprint `json:"blueprints"`
	}
	err := c.Do(ctx, http.MethodGet, "/api/v1/blueprints", nil, &resp)
	return resp.Blueprints, err
}

// SpecResult is the response of PUT /api/v1/project/spec.
type SpecResult struct {
	ReceivedAt  string `json:"received_at"`
	SpecVersion int    `json:"spec_version"`
	Changed     bool   `json:"changed"`
}

// PushSpec sends the project's resolved spec. The client's token must be the
// project's token.
func (c *Client) PushSpec(ctx context.Context, spec json.RawMessage) (SpecResult, error) {
	var res SpecResult
	err := c.Do(ctx, http.MethodPut, "/api/v1/project/spec", map[string]json.RawMessage{"spec": spec}, &res)
	return res, err
}

// Upload is a document to publish: one HTML file, or a zip bundle with the
// path of its HTML entry point.
type Upload struct {
	Name           string // file name sent to the server
	Data           []byte
	Bundle         bool
	Entrypoint     string // with Bundle
	Slug           string
	Title          string
	Description    string
	Kind           string
	PasswordSet    bool
	DescriptionSet bool
	Password       string
	Visibility     string
	Readable       *bool // nil keeps the document's current setting
}

// Document is the response of POST /api/v1/documents.
type Document struct {
	ID                 int     `json:"id"`
	CollectionID       int     `json:"collection_id,omitempty"`
	CollectionAttached *bool   `json:"collection_attached,omitempty"`
	CollectionWarning  string  `json:"collection_warning,omitempty"`
	Slug               string  `json:"slug"`
	Version            int     `json:"version"`
	URL                string  `json:"url"`
	PrivateURL         string  `json:"private_url"`
	ReadableURL        *string `json:"readable_url"`
	VersionURL         string  `json:"version_url"`
	RawURL             string  `json:"raw_url"`
	Notice             *string `json:"notice"`
}

// Collection is a project collection available to this project token.
type Collection struct {
	ID         int    `json:"id"`
	Slug       string `json:"slug"`
	Title      string `json:"title"`
	Visibility string `json:"visibility"`
}

func (c *Client) Collections(ctx context.Context) ([]Collection, error) {
	var res struct {
		Collections []Collection `json:"collections"`
	}
	err := c.Do(ctx, http.MethodGet, "/api/v1/collections", nil, &res)
	return res.Collections, err
}

func (c *Client) AttachCollection(ctx context.Context, collectionID, documentID int, shareUnlisted bool) error {
	var res map[string]any
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/collections/%d/items", collectionID),
		map[string]any{"document_id": documentID, "share_unlisted": shareUnlisted}, &res)
}

// Publish uploads a document to the project the client's token belongs to.
// Publishing to an existing slug adds a version.
func (c *Client) Publish(ctx context.Context, u Upload) (Document, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	field := "file"
	if u.Bundle {
		field = "bundle"
	}
	part, err := form.CreateFormFile(field, u.Name)
	if err != nil {
		return Document{}, err
	}
	if _, err := part.Write(u.Data); err != nil {
		return Document{}, err
	}
	fields := map[string]string{"slug": u.Slug, "title": u.Title, "description": u.Description, "kind": u.Kind, "password": u.Password, "visibility": u.Visibility}
	if u.Bundle {
		fields["entrypoint"] = u.Entrypoint
	}
	if u.Readable != nil {
		// Laravel's boolean rule takes 1 and 0 in form fields.
		fields["readable"] = "0"
		if *u.Readable {
			fields["readable"] = "1"
		}
	}
	for k, v := range fields {
		explicitEmpty := (k == "password" && u.PasswordSet) || (k == "description" && u.DescriptionSet)
		if v == "" && !explicitEmpty {
			continue
		}
		if err := form.WriteField(k, v); err != nil {
			return Document{}, err
		}
	}
	if err := form.Close(); err != nil {
		return Document{}, err
	}
	// Uploads can be large, so they get more time than other requests.
	up := *c
	up.HTTP = &http.Client{Timeout: 10 * time.Minute, Transport: c.HTTP.Transport}
	var doc Document
	err = up.send(ctx, http.MethodPost, "/api/v1/documents", form.FormDataContentType(), &body, &doc)
	return doc, err
}

// send is Do with a raw body of the given content type.
func (c *Client) send(ctx context.Context, method, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	return c.do(req, out)
}

// ErrPaymentRequired explains a 402 from publishing.
func ErrPaymentRequired(host string) error {
	return fmt.Errorf("this account cannot publish documents on %s. Publishing on framework.pub needs paid hosting; self-hosted servers allow it", host)
}
