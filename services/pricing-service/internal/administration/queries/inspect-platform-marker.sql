-- $1 environment, $2 instance UUID, $3 cluster UID, $4 seed allowed.
-- Run only after inspect-platform.sql verifies the marker relation and its owner.
SELECT count(*) = 1 AND coalesce(bool_and(
    singleton AND environment = $1 AND instance_id = $2::uuid
    AND cluster_uid = $3 AND seed_allowed = $4
), false)
FROM pricing_admin.target_identity;
