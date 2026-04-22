-- v0.5 PR D — per-user bucket weights for the similarity engine.
--
-- Adds bucket_weights as a JSON column on the existing
-- user_similarity_settings row (one-row-per-user, migration 0008).
-- NULL column = "user has tuned nothing" — the engine falls
-- through to each Bucket.DefaultWeight() as before.
--
-- JSON rather than a normalized user_bucket_weights(user_id,
-- bucket_id, weight) table because bucket IDs are owned by code
-- (no FK possible), adding a new bucket doesn't require a
-- migration (the JSON just gains a key at next write), and
-- read-path is one json_extract or one Go unmarshal. The v0.6+
-- plugin bucket system relies on this — a plugin that registers
-- a new bucket ID at runtime must not require a schema change.
--
-- Schema: {"discogs.same_artist": 5.0, "discogs.same_label_era": 3.0,
--          "events.same_festival": 2.5, ...}. Keys are bucket IDs;
-- values are float weights (0..10 by convention; 0 = disabled;
-- negative coerces to 0 in the engine). Missing keys fall back to
-- DefaultWeight() in the engine so the JSON can be SPARSE —
-- users only need to persist the buckets they actually tuned.

ALTER TABLE user_similarity_settings
    ADD COLUMN bucket_weights TEXT;
