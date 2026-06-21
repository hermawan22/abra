ALTER TABLE learning_proposals
  DROP CONSTRAINT IF EXISTS learning_proposals_status_check;

ALTER TABLE learning_proposals
  ADD CONSTRAINT learning_proposals_status_check
  CHECK (status IN ('pending', 'accepted', 'applying', 'rejected', 'applied', 'canceled'));
