-- Backfill publication timestamps so already-published galleries appear in
-- feeds. Going forward, publishing a gallery stamps published_at automatically.
UPDATE galleries
   SET published_at = COALESCE(published_at, updated_at, created_at)
 WHERE status = 'published' AND published_at IS NULL;
