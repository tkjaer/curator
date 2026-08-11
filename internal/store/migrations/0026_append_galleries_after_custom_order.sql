WITH mixed_parents AS (
    SELECT parent_id, MAX(sort_order) AS max_order
    FROM galleries
    GROUP BY parent_id
    HAVING MAX(sort_order) > 0 AND MIN(sort_order) = 0
), appended AS (
    SELECT g.id,
           m.max_order + ROW_NUMBER() OVER (PARTITION BY g.parent_id ORDER BY g.id) AS new_order
    FROM galleries g
    JOIN mixed_parents m ON g.parent_id IS m.parent_id
    WHERE g.sort_order = 0
)
UPDATE galleries
SET sort_order = (SELECT new_order FROM appended WHERE appended.id = galleries.id),
    updated_at = datetime('now')
WHERE id IN (SELECT id FROM appended);
