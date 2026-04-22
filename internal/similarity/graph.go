package similarity

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// v0.7 graph view: an exploratory canvas rooted on the
// currently-playing song with up to N neighbors drawn from a
// small, fixed subset of the bucket registry. Unlike NextPick
// (which runs all registered buckets through the full merge +
// filter + weighted-pick pipeline), the graph endpoint is a
// curated preview of "outside connections" — same-label and
// collaborator buckets only, same-artist candidates explicitly
// dropped, no score ranking (first-found wins, interleaved
// between the two buckets).
//
// No persistence layer: the graph is computed on demand from
// the Discogs-cached bucket output. Same (center song, limit)
// combo re-requested in the 30-day cache window costs 0 API
// calls.

// DefaultGraphLimit is the starting node count for the v0.7
// graph view. User can override per-call via the limit query
// param (PR A-B) or persistently via Settings (PR C).
const DefaultGraphLimit = 8

// MaxGraphLimit caps the upper bound — beyond this the star
// layout gets unreadable and the underlying Discogs calls
// start approaching rate limits. Keep it loose but bounded.
const MaxGraphLimit = 30

// Two bucket IDs the graph view pulls from. Kept as constants
// (not an env knob) because the color mapping on the frontend
// is hard-coded to match; changing here without changing the
// frontend would render mystery-colored edges.
const (
	graphBucketLabel        = "discogs.same_label_era"
	graphBucketCollaborators = "discogs.collaborators"
)

// GraphResult is the JSON shape returned by
// GET /api/similarity/graph. Cytoscape-friendly: the Nodes
// slice includes the center as its first element with
// IsCenter=true so the frontend can apply the star layout
// without a second lookup.
type GraphResult struct {
	Center GraphNode   `json:"center"`
	Nodes  []GraphNode `json:"nodes"`
	Edges  []GraphEdge `json:"edges"`
}

// GraphNode is one song in the graph. ID is a stable string —
// the center's queue_songs UUID for the center, a
// "lower(artist)|lower(title)" compound key for candidates
// (matches the engine's candidate key, so two buckets proposing
// the same track produce ONE node with two edges).
type GraphNode struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	ImageURL string `json:"imageUrl,omitempty"`
	IsCenter bool   `json:"isCenter,omitempty"`
}

// GraphEdge is one (candidate → center) connection produced by
// a specific bucket. A candidate returned by both buckets gets
// two edges (different Bucket values) but one node in Nodes.
type GraphEdge struct {
	Source string         `json:"source"` // candidate node id
	Target string         `json:"target"` // center node id
	Bucket string         `json:"bucket"`
	Meta   map[string]any `json:"meta,omitempty"`
}

// ExploreGraph builds the v0.7 graph rooted at songID. Runs the
// two configured buckets (same_label_era, collaborators),
// excludes same-artist candidates, interleaves the two bucket
// outputs 50/50 up to `limit`, and falls back to the larger
// pool when one bucket is short.
//
// Errors propagate when the center song can't be read (missing
// queue_songs row); everything else (empty buckets, missing
// Discogs hydration, buckets unregistered) produces an empty
// or partial graph rather than an error — the UI needs a
// renderable result even in degraded states.
func (s *Service) ExploreGraph(ctx context.Context, userID, songID uuid.UUID, limit int) (GraphResult, error) {
	if limit <= 0 {
		limit = DefaultGraphLimit
	}
	if limit > MaxGraphLimit {
		limit = MaxGraphLimit
	}
	if s.seedReader == nil {
		return GraphResult{}, fmt.Errorf("similarity: seed reader not wired")
	}
	seed, err := s.seedReader.ReadSeed(ctx, userID, songID)
	if err != nil {
		return GraphResult{}, fmt.Errorf("similarity: read center: %w", err)
	}
	center := GraphNode{
		ID:       songID.String(),
		Title:    seed.Title,
		Artist:   seed.Artist,
		IsCenter: true,
	}
	// Seed metadata can be empty (Bandcamp-only, orphan row);
	// return just the center with no neighbors so the UI can
	// render its "no Discogs match" empty state.
	if seed.Title == "" || seed.Artist == "" {
		return GraphResult{Center: center, Nodes: []GraphNode{center}, Edges: []GraphEdge{}}, nil
	}

	labelBucket, collabBucket := s.lookupGraphBuckets()

	// Parallel fan-out.
	var (
		labelCands  []Candidate
		collabCands []Candidate
		wg          sync.WaitGroup
	)
	if labelBucket != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cs, _ := labelBucket.Candidates(ctx, seed)
			labelCands = dropSameArtist(cs, seed.Artist)
		}()
	}
	if collabBucket != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cs, _ := collabBucket.Candidates(ctx, seed)
			collabCands = dropSameArtist(cs, seed.Artist)
		}()
	}
	wg.Wait()

	nodes, edges := composeGraph(center, labelCands, collabCands, limit)
	return GraphResult{
		Center: center,
		Nodes:  nodes,
		Edges:  edges,
	}, nil
}

// lookupGraphBuckets snapshots the current registry for the two
// buckets the graph view uses. Either can be nil (unregistered,
// e.g. no Discogs client) — composeGraph handles missing buckets.
func (s *Service) lookupGraphBuckets() (Bucket, Bucket) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var label, collab Bucket
	for _, b := range s.buckets {
		switch b.ID() {
		case graphBucketLabel:
			label = b
		case graphBucketCollaborators:
			collab = b
		}
	}
	return label, collab
}

// dropSameArtist filters out candidates whose artist matches
// `artist` case-insensitively. The v0.7 graph deliberately
// omits same-artist connections — users want to explore
// outward, and the same_artist bucket's output (if we ran it)
// would drown everything else.
func dropSameArtist(cs []Candidate, artist string) []Candidate {
	want := strings.ToLower(strings.TrimSpace(artist))
	if want == "" {
		return cs
	}
	out := make([]Candidate, 0, len(cs))
	for _, c := range cs {
		if strings.ToLower(strings.TrimSpace(c.Artist)) == want {
			continue
		}
		out = append(out, c)
	}
	return out
}

// composeGraph assembles the final node/edge slices. Target
// shape per the v0.7 spec: equal split between label and
// collab buckets, shortfall in one filled from the other,
// both short ⇒ graph is smaller than limit. First-found order
// within each bucket; no score ranking.
func composeGraph(center GraphNode, label, collab []Candidate, limit int) ([]GraphNode, []GraphEdge) {
	// Plan the per-bucket take.
	half := limit / 2
	takeLabel := minInt(half, len(label))
	takeCollab := minInt(limit-half, len(collab))
	// Shortfall fill: if one bucket didn't hit its quota, fill
	// from the other's unused surplus.
	shortfall := limit - takeLabel - takeCollab
	if shortfall > 0 {
		if extra := len(label) - takeLabel; extra > 0 {
			add := minInt(shortfall, extra)
			takeLabel += add
			shortfall -= add
		}
	}
	if shortfall > 0 {
		if extra := len(collab) - takeCollab; extra > 0 {
			add := minInt(shortfall, extra)
			takeCollab += add
		}
	}

	// Merge with interleaving so the rendered graph shows both
	// colors even when one bucket happens to be returned first.
	merged := interleaveFirstN(label[:takeLabel], collab[:takeCollab])

	nodeByID := make(map[string]GraphNode, len(merged))
	nodes := make([]GraphNode, 0, len(merged)+1)
	edges := make([]GraphEdge, 0, len(merged)+4)

	nodes = append(nodes, center)

	// Emit edges; dedupe nodes by (artist, title) key across
	// buckets so candidates in BOTH get a single node with two
	// edges. The first contributing bucket's ImageURL wins.
	for _, m := range merged {
		nodeID := candidateNodeKey(m.c.Artist, m.c.Title)
		if _, ok := nodeByID[nodeID]; !ok {
			nodeByID[nodeID] = GraphNode{
				ID:       nodeID,
				Title:    m.c.Title,
				Artist:   m.c.Artist,
				ImageURL: m.c.ImageURL,
			}
			nodes = append(nodes, nodeByID[nodeID])
		}
		edges = append(edges, GraphEdge{
			Source: nodeID,
			Target: center.ID,
			Bucket: m.bucketID,
			Meta:   m.c.Edge,
		})
	}
	return nodes, edges
}

// mergedCandidate pairs a Candidate with the bucket id it came
// from so interleaveFirstN can preserve both bucket
// attribution and the dedup key.
type mergedCandidate struct {
	c        Candidate
	bucketID string
}

// interleaveFirstN round-robins between the two bucket outputs.
// After round-robin the dedup key is (artist, title); duplicates
// get their edges combined in composeGraph via the nodeByID map
// downstream.
func interleaveFirstN(label, collab []Candidate) []mergedCandidate {
	out := make([]mergedCandidate, 0, len(label)+len(collab))
	i, j := 0, 0
	for i < len(label) || j < len(collab) {
		if i < len(label) {
			out = append(out, mergedCandidate{c: label[i], bucketID: graphBucketLabel})
			i++
		}
		if j < len(collab) {
			out = append(out, mergedCandidate{c: collab[j], bucketID: graphBucketCollaborators})
			j++
		}
	}
	return out
}

// candidateNodeKey is the stable string id for a candidate.
// Must be usable unmodified by Cytoscape's string comparison
// for source/target lookups — avoid special chars.
func candidateNodeKey(artist, title string) string {
	return "cand|" + strings.ToLower(strings.TrimSpace(artist)) + "|" +
		strings.ToLower(strings.TrimSpace(title))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
