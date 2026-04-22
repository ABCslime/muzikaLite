package similarity

// Preset is a named bucket-weight configuration exposed via
// GET /api/similarity/presets. The frontend's Settings panel
// offers these as a dropdown — clicking Apply populates the
// weight sliders in the draft, the user fine-tunes if they
// want, then Save persists via the existing PUT /weights
// route. Presets are therefore ephemeral hints, not a
// persistently-selected "mode."
//
// Preset IDs are stable; Label + Description are i18n-safe
// English strings until we ship translations. Weights use
// the same bucket IDs as the registry — unknown keys in a
// preset are inert (only registered buckets see their
// corresponding weight).
type Preset struct {
	ID          string             `json:"id"`
	Label       string             `json:"label"`
	Description string             `json:"description"`
	Weights     map[string]float64 `json:"weights"`
}

// builtinPresets is the v0.6 starter set. Three presets,
// picked to span the "how adventurous is the mix?" axis:
//
//   Familiar   — same artist + label dominate, minimal drift.
//                For "I want more of exactly this."
//   Discovery  — same artist dropped low, style and
//                collaborators boosted. For "show me neighbors,
//                not the same thing I picked."
//   Obscure    — collaborators and style maxed, genre off.
//                For "send me somewhere weird."
//
// All presets use the five v0.5-built-in bucket IDs. v0.6
// plugin buckets don't appear here because their IDs aren't
// known at code time — the frontend renders the preset's
// weights over the live bucket list; unknown-bucket keys just
// don't show up as sliders.
var builtinPresets = []Preset{
	{
		ID:          "familiar",
		Label:       "Familiar",
		Description: "Stay close to the seed's artist and label. Minimal drift into new territory.",
		Weights: map[string]float64{
			"discogs.same_artist":    8,
			"discogs.same_label_era": 5,
			"discogs.same_style_era": 1,
			"discogs.collaborators":  2,
			"discogs.same_genre_era": 0,
		},
	},
	{
		ID:          "discovery",
		Label:       "Discovery",
		Description: "Prefer neighbors over the same artist. Widens into the seed's scene.",
		Weights: map[string]float64{
			"discogs.same_artist":    1,
			"discogs.same_label_era": 3,
			"discogs.same_style_era": 5,
			"discogs.collaborators":  5,
			"discogs.same_genre_era": 3,
		},
	},
	{
		ID:          "obscure",
		Label:       "Obscure",
		Description: "Max collaborators and sub-genre; skip broad-genre matches entirely.",
		Weights: map[string]float64{
			"discogs.same_artist":    1,
			"discogs.same_label_era": 2,
			"discogs.same_style_era": 5,
			"discogs.collaborators":  8,
			"discogs.same_genre_era": 0,
		},
	},
}

// Presets returns a copy of the builtin preset list. Returned
// weights maps are fresh per call so callers can mutate without
// affecting the package-level definitions.
func Presets() []Preset {
	out := make([]Preset, len(builtinPresets))
	for i, p := range builtinPresets {
		copied := make(map[string]float64, len(p.Weights))
		for k, v := range p.Weights {
			copied[k] = v
		}
		out[i] = Preset{
			ID:          p.ID,
			Label:       p.Label,
			Description: p.Description,
			Weights:     copied,
		}
	}
	return out
}
