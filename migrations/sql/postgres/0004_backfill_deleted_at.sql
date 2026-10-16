-- Backfill deleted_at for objects that were removed before it was recorded.
--
-- deleted_at is the marker that takes an object out of the stored set. ListObjects
-- filters on `deleted_at IS NULL`, and the diagnostics count does too.
--
-- The column existed from the start but UpdateStatus never wrote it, so every object
-- deleted before that was fixed is still indistinguishable from a live one. On the
-- development database that meant ten deleted objects still being listed after the
-- delete in the console appeared to succeed -- the exact confusion this repairs.
--
-- Only rows that are already terminal are touched, and only when deleted_at is still
-- NULL, so this is safe to run once and cannot resurrect anything. updated_at is the
-- only timestamp available for when the removal happened, and it is the closest
-- honest answer: it is the moment the row last changed, which for these rows is the
-- delete itself.
--
-- 'deleted' and 'missing' are both included: both mean the object is no longer part of
-- the stored set. 'missing' is only ever set by the delete path, when the provider has
-- already lost the file.

UPDATE storage_objects
   SET deleted_at = updated_at
 WHERE deleted_at IS NULL
   AND status IN ('deleted', 'missing');
