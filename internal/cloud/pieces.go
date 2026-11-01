package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// Piece is one entry of the lists of blueprint pieces: project structures,
// tools, package sets, skill sets, and skills. Fields a kind lacks stay
// empty. Raw keeps the server's object for --json output.
type Piece struct {
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Type        string          `json:"type"`
	Language    Languages       `json:"language"`
	Requires    []string        `json:"requires"`
	Packages    []string        `json:"packages"`
	Skills      []string        `json:"skills"`
	Version     int             `json:"version"`
	System      bool            `json:"system"`
	Raw         json.RawMessage `json:"-"`
}

// Languages is a piece's language: one for package and skill sets, a list
// for tools.
type Languages []string

func (l *Languages) UnmarshalJSON(data []byte) error {
	var one string
	if json.Unmarshal(data, &one) == nil {
		if one != "" {
			*l = Languages{one}
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*l = many
	return nil
}

// Pieces lists one kind of piece, such as "structures" or "package-sets".
// field is the response's key, such as "package_sets". language filters
// when it is not empty.
func (c *Client) Pieces(ctx context.Context, path, field, language string) ([]Piece, error) {
	p := "/api/v2/" + path
	if language != "" {
		p += "?language=" + url.QueryEscape(language)
	}
	var resp map[string][]json.RawMessage
	if err := c.Do(ctx, http.MethodGet, p, nil, &resp); err != nil {
		return nil, err
	}
	pieces := make([]Piece, 0, len(resp[field]))
	for _, raw := range resp[field] {
		var piece Piece
		if err := json.Unmarshal(raw, &piece); err != nil {
			return nil, err
		}
		piece.Raw = raw
		pieces = append(pieces, piece)
	}
	return pieces, nil
}

// Choices are what a new project is built from: a blueprint, a project
// structure, or both, with tools, sets, skills, and settings. Framework
// Cloud fills what is left out from the user's defaults.
type Choices struct {
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Language    string          `json:"language,omitempty"`
	Blueprint   string          `json:"blueprint,omitempty"`
	Structure   string          `json:"structure,omitempty"`
	Tools       map[string]bool `json:"tools,omitempty"`
	PackageSets []string        `json:"package_sets,omitempty"`
	SkillSets   []string        `json:"skill_sets,omitempty"`
	Skills      []string        `json:"skills,omitempty"`
	Settings    map[string]any  `json:"settings,omitempty"`
}

// Resolve turns choices into a layers envelope without creating a project.
func (c *Client) Resolve(ctx context.Context, choices Choices) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.Do(ctx, http.MethodPost, "/api/v2/resolve", choices, &raw)
	return raw, err
}

// CreatedProject is the response of POST /api/v2/projects. Token is a
// project token for linking the local project.
type CreatedProject struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Language string `json:"language"`
	URL      string `json:"url"`
	Token    string `json:"token"`
}

// CreateProject creates a project on Framework Cloud from choices, as the
// website does.
func (c *Client) CreateProject(ctx context.Context, choices Choices) (CreatedProject, error) {
	var p CreatedProject
	err := c.Do(ctx, http.MethodPost, "/api/v2/projects", choices, &p)
	return p, err
}
