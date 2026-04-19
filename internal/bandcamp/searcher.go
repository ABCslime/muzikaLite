package bandcamp

import "context"

// SearchResult is a single discover-endpoint hit.
type SearchResult struct {
	Title  string
	Artist string
}

// Search returns one random song matching `genre`. Empty genre falls back to
// the client's default tag list.
// TODO(port): Phase 7.
func (c *Client) Search(ctx context.Context, genre string) (SearchResult, error) {
	return SearchResult{}, ErrNotImplemented
}
