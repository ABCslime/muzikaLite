-- v0.7 PR C rollback.

ALTER TABLE user_similarity_settings
    DROP COLUMN graph_node_limit;
