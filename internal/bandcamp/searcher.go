package bandcamp

import (
	"context"
	"errors"
	"strings"
)

// SearchResult is a single discover-endpoint hit.
type SearchResult struct {
	Title  string
	Artist string
}

// Search returns one random song for `genre`. Empty genre falls back to a
// random pick from the client's default tag list. If no results come back,
// ErrNoResults is returned — caller may retry with a different tag.
func (c *Client) Search(ctx context.Context, genre string) (SearchResult, error) {
	tag := strings.TrimSpace(genre)
	if tag == "" {
		tag = c.pickDefaultTag()
	}
	if tag == "" {
		return SearchResult{}, errors.New("bandcamp: no genre and no default tags configured")
	}

	resp, err := c.discover(ctx, tag)
	if err != nil {
		return SearchResult{}, err
	}
	if len(resp.Results) == 0 {
		return SearchResult{}, ErrNoResults
	}
	idx := c.rng.Intn(len(resp.Results))
	it := resp.Results[idx]
	return SearchResult{
		Title:  it.Title,
		Artist: it.BandName,
	}, nil
}

func (c *Client) pickDefaultTag() string {
	if len(c.defaultTags) == 0 {
		return ""
	}
	return c.defaultTags[c.rng.Intn(len(c.defaultTags))]
}
