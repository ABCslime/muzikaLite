-- v0.7 PR C — per-user graph-view settings.
--
-- Adds graph_node_limit as an integer column on the existing
-- user_similarity_settings row (one-row-per-user from
-- migration 0008). NULL = "user hasn't tuned" — similarity
-- service falls through to DefaultGraphLimit (8) code-side.
--
-- Integer instead of a JSON blob because v0.7 only needs the
-- one knob. If v0.7.x adds more graph settings (layout kind,
-- edge-color overrides, etc.) this column stays and siblings
-- land alongside it.
--
-- Range enforcement (1..MaxGraphLimit=30) happens in the
-- repo and handler, not as a CHECK constraint — keeps the
-- migration a simple ADD COLUMN and doesn't require a rewrite
-- if the range changes later.

ALTER TABLE user_similarity_settings
    ADD COLUMN graph_node_limit INTEGER;
