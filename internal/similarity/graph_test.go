package similarity

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestComposeGraph_EqualSplit: both buckets have more than
// half the limit — we take exactly half from each.
func TestComposeGraph_EqualSplit(t *testing.T) {
	label := fakeCandidates("L", 6)
	collab := fakeCandidates("C", 6)
	_, edges := composeGraph(centerNode(), label, collab, 8)
	countL, countC := countByBucket(edges)
	if countL != 4 || countC != 4 {
		t.Errorf("expected 4/4, got label=%d collab=%d", countL, countC)
	}
}

// TestComposeGraph_ShortfallFills: label only has 2 matches,
// collab has 10 — the 2 shortfall fills from collab so we
// still hit limit=8. Final mix: 2 label + 6 collab.
func TestComposeGraph_ShortfallFills(t *testing.T) {
	label := fakeCandidates("L", 2)
	collab := fakeCandidates("C", 10)
	_, edges := composeGraph(centerNode(), label, collab, 8)
	countL, countC := countByBucket(edges)
	if countL != 2 || countC != 6 {
		t.Errorf("expected 2/6 shortfall fill, got label=%d collab=%d", countL, countC)
	}
}

// TestComposeGraph_BothShort: when both buckets are below
// their quotas the graph just contains whatever they returned.
// No error, no padding.
func TestComposeGraph_BothShort(t *testing.T) {
	label := fakeCandidates("L", 2)
	collab := fakeCandidates("C", 3)
	nodes, _ := composeGraph(centerNode(), label, collab, 8)
	// 5 unique candidates + 1 center = 6 nodes.
	if len(nodes) != 6 {
		t.Errorf("expected 6 nodes (5 cands + center), got %d", len(nodes))
	}
}

// TestComposeGraph_DedupeAcrossBuckets: a candidate that
// appears in BOTH buckets produces one node + two edges.
func TestComposeGraph_DedupeAcrossBuckets(t *testing.T) {
	shared := Candidate{Title: "Shared", Artist: "Both"}
	label := []Candidate{shared}
	collab := []Candidate{shared}
	nodes, edges := composeGraph(centerNode(), label, collab, 8)
	// 1 candidate + 1 center = 2 nodes; 2 edges to center.
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes (1 cand + center), got %d", len(nodes))
	}
	if len(edges) != 2 {
		t.Errorf("expected 2 edges (cand → center via both buckets), got %d", len(edges))
	}
}

// TestDropSameArtist removes (case-insensitive) same-artist
// candidates from the bucket output before composeGraph even
// sees them.
func TestDropSameArtist(t *testing.T) {
	cs := []Candidate{
		{Title: "a", Artist: "Daft Punk"},
		{Title: "b", Artist: "DAFT PUNK"}, // case drift
		{Title: "c", Artist: "Justice"},
	}
	got := dropSameArtist(cs, "daft punk")
	if len(got) != 1 || got[0].Title != "c" {
		t.Errorf("expected only Justice to survive; got %+v", got)
	}
}

// --- helpers ---

func fakeCandidates(prefix string, n int) []Candidate {
	out := make([]Candidate, n)
	for i := 0; i < n; i++ {
		out[i] = Candidate{
			Title:  prefix + string(rune('0'+i)),
			Artist: prefix + "-artist",
		}
	}
	return out
}

func centerNode() GraphNode {
	return GraphNode{ID: uuid.New().String(), Title: "Center", Artist: "Seed Artist", IsCenter: true}
}

func countByBucket(edges []GraphEdge) (label, collab int) {
	for _, e := range edges {
		switch e.Bucket {
		case graphBucketLabel:
			label++
		case graphBucketCollaborators:
			collab++
		}
	}
	return
}

// TestExploreGraph_EmptyWhenSeedHasNoMetadata covers the
// Bandcamp-only / orphan-row case: graph returns just the
// center with no neighbors.
func TestExploreGraph_EmptyWhenSeedHasNoMetadata(t *testing.T) {
	s := NewService(Config{
		SeedReader: &stubSeedReader{seed: Seed{}}, // empty title+artist
	})
	g, err := s.ExploreGraph(context.Background(), uuid.New(), uuid.New(), 8)
	if err != nil {
		t.Fatalf("ExploreGraph: %v", err)
	}
	if len(g.Edges) != 0 {
		t.Errorf("expected 0 edges for empty seed, got %d", len(g.Edges))
	}
}
