INSERT OR IGNORE INTO tag_map (tag_id, item_id)
SELECT canonical.id, tag_map.item_id
  FROM tags AS duplicate
  JOIN tag_map ON tag_map.tag_id = duplicate.id
  JOIN tags AS canonical
    ON canonical.namespace = 'user'
   AND lower(canonical.value) = lower(duplicate.value)
   AND canonical.id = (
       SELECT min(candidate.id)
         FROM tags AS candidate
        WHERE candidate.namespace = 'user'
          AND lower(candidate.value) = lower(duplicate.value)
   )
 WHERE duplicate.namespace = 'user';

DELETE FROM tag_map
 WHERE tag_id IN (
     SELECT duplicate.id
       FROM tags AS duplicate
      WHERE duplicate.namespace = 'user'
        AND duplicate.id != (
            SELECT min(candidate.id)
              FROM tags AS candidate
             WHERE candidate.namespace = 'user'
               AND lower(candidate.value) = lower(duplicate.value)
        )
 );

DELETE FROM tags
 WHERE namespace = 'user'
   AND id != (
       SELECT min(candidate.id)
         FROM tags AS candidate
        WHERE candidate.namespace = 'user'
          AND lower(candidate.value) = lower(tags.value)
   );

UPDATE tags SET value = lower(value) WHERE namespace = 'user';
UPDATE settings SET value = lower(value) WHERE key = 'metadata.tag_selection';