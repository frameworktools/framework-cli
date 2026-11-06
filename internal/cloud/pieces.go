package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
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
// when it is not empty. Some servers ignore ?language= for some kinds, so
// fw also drops pieces that name other languages only. A piece that names
// no language works with all of them.
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
		if language != "" && len(piece.Language) > 0 && !slices.Contains(piece.Language, language) {
			continue
		}
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
	// Workflow is quarto, jupyter, or scripts; empty uses the person's default.
	Workflow string `json:"workflow,omitempty"`
	// GitHub, when creating a project, also scaffolds it in a new repository.
	// The server ignores it when resolving.
	GitHub *NewRepository `json:"github,omitempty"`
}

// NewRepository is where POST /api/v2/projects scaffolds the project on
// GitHub. An empty Name uses the project's slug.
type NewRepository struct {
	Owner   string `json:"owner"`
	Name    string `json:"name,omitempty"`
	Private bool   `json:"private"`
}

// DefaultsRevisionHeader names the response header that identifies the
// version of the person's defaults. It is opaque: compare it for equality.
const DefaultsRevisionHeader = "Framework-Defaults-Revision"

// NoticeHeader names the response header that carries one notice, plain
// text explaining what the server did, such as setting a default aside.
const NoticeHeader = "Framework-Notice"

// Resolution is the response of POST /api/v2/resolve.
type Resolution struct {
	Layers   json.RawMessage
	Revision string   // the defaults revision
	Notices  []string // for the person to read
}

// Resolve turns choices into a layers envelope without creating a project.
// Choices with only a language resolve from the person's defaults.
func (c *Client) Resolve(ctx context.Context, choices Choices) (Resolution, error) {
	data, err := json.Marshal(choices)
	if err != nil {
		return Resolution{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/api/v2/resolve", bytes.NewReader(data))
	if err != nil {
		return Resolution{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var r Resolution
	header, err := c.doHeader(req, &r.Layers)
	if err != nil {
		return Resolution{}, err
	}
	r.Revision = header.Get(DefaultsRevisionHeader)
	r.Notices = header.Values(NoticeHeader)
	return r, nil
}

// CreatedProject is the response of POST /api/v2/projects. Token is a
// project token for linking the local project.
type CreatedProject struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Language string `json:"language"`
	URL      string `json:"url"`
	Token    string `json:"token"`
	// GitHub is set when the project is being scaffolded on GitHub.
	GitHub *struct {
		Repository     string `json:"repository"`
		ScaffoldStatus string `json:"scaffold_status"`
	} `json:"github"`
}

// CreateProject creates a project on Framework Cloud from choices, as the
// website does.
func (c *Client) CreateProject(ctx context.Context, choices Choices) (CreatedProject, error) {
	var p CreatedProject
	err := c.Do(ctx, http.MethodPost, "/api/v2/projects", choices, &p)
	return p, err
}

// ExtraDirectory is a folder outside Framework's directory keys.
type ExtraDirectory struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// NewStructure is the body of POST /api/v2/structures: a project structure
// made from an existing project's folders.
type NewStructure struct {
	Name             string                    `json:"name"`
	Description      string                    `json:"description,omitempty"`
	Directories      map[string]string         `json:"directories"`
	ExtraDirectories map[string]ExtraDirectory `json:"extra_directories,omitempty"`
}

// CreatedStructure is the response of POST /api/v2/structures. The
// structure starts private.
type CreatedStructure struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// CreateStructure saves a project structure on Framework Cloud.
func (c *Client) CreateStructure(ctx context.Context, s NewStructure) (CreatedStructure, error) {
	var out CreatedStructure
	err := c.Do(ctx, http.MethodPost, "/api/v2/structures", s, &out)
	return out, err
}
