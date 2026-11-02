package cloud

import (
	"context"
	"net/http"
	"net/url"
)

// Destinations is GET /api/v2/destinations: the ways the server can hand
// over a new project besides building it on this computer.
type Destinations struct {
	Download struct {
		Available bool `json:"available"`
	} `json:"download"`
	GitHub struct {
		Available bool   `json:"available"`
		Login     string `json:"login"`
		// Owners is nil when the server could not reach GitHub.
		Owners []struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"owners"`
		InstallURL string `json:"install_url"`
		ConnectURL string `json:"connect_url"`
		// Unavailable maps a language to why GitHub can't scaffold it here.
		Unavailable map[string]string `json:"unavailable"`
	} `json:"github"`
}

// Destinations asks the server what it can do with a new project. Servers
// from before downloads answer 404.
func (c *Client) Destinations(ctx context.Context) (Destinations, error) {
	var d Destinations
	err := c.Do(ctx, http.MethodGet, "/api/v2/destinations", nil, &d)
	return d, err
}

// DownloadProject returns the project built by the server, as a zip whose
// files sit under a folder named after the project's slug.
func (c *Client) DownloadProject(ctx context.Context, slug string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/api/v2/projects/"+url.PathEscape(slug)+"/download", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/zip, application/json")
	_, data, err := c.doRaw(req)
	return data, err
}
