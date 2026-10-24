SET NAMES utf8mb4;
SET time_zone = '+08:00';

-- Normalize catalog labels first. Existing adapters may have stored UNKNOWN while
-- the upstream profile key still identifies main/sub/third streams.
UPDATE camera_stream_profile
SET usage_origin = 'MANUAL',
    usage_hint = CASE
        WHEN LOWER(CONCAT_WS(' ', label, source_label, external_profile_key)) REGEXP '(^|[^a-z])sub[ -]?stream([^a-z]|$)'
          THEN 'SUB'
        WHEN LOWER(CONCAT_WS(' ', label, source_label, external_profile_key)) REGEXP '(^|[^a-z])third[ -]?stream([^a-z]|$)'
          THEN 'THIRD'
        WHEN LOWER(CONCAT_WS(' ', label, source_label, external_profile_key)) REGEXP '(^|[^a-z])main[ -]?stream([^a-z]|$)'
          THEN 'MAIN'
        ELSE usage_hint
    END,
    version = version + 1,
    updated_at = CURRENT_TIMESTAMP(6)
WHERE usage_hint = 'UNKNOWN'
  AND LOWER(CONCAT_WS(' ', label, source_label, external_profile_key)) REGEXP '(^|[^a-z])(sub|main|third)[ -]?stream([^a-z]|$)';

-- A sub stream becomes the default only when the current default is absent,
-- unknown, or explicitly the old third-stream fallback. A deliberate MAIN/other
-- selection remains untouched.
UPDATE camera_channel c
JOIN (
    SELECT channel_id, id,
           ROW_NUMBER() OVER (PARTITION BY channel_id ORDER BY id) AS stream_order
    FROM camera_stream_profile
    WHERE enabled = 1 AND usage_hint = 'SUB'
) sub_profile ON sub_profile.channel_id = c.id AND sub_profile.stream_order = 1
LEFT JOIN camera_stream_profile current_profile
  ON current_profile.id = c.default_preview_profile_id
SET c.default_preview_profile_id = sub_profile.id,
    c.version = c.version + 1,
    c.updated_at = CURRENT_TIMESTAMP(6)
WHERE c.default_preview_profile_id IS NULL
   OR current_profile.usage_hint IN ('UNKNOWN', 'THIRD');
