-- Hot-path indexes for stored agent-action policy evaluation.
-- Working-memory composition evaluates standard risky actions on every smart
-- path request, so keep lookup and ordering scoped to agent policies.

CREATE INDEX IF NOT EXISTS policies_agent_action_scope_priority_idx
  ON policies (scope, priority ASC, created_at DESC)
  WHERE policy_type = 'agent_action';

CREATE INDEX IF NOT EXISTS policies_agent_action_scope_subject_priority_idx
  ON policies (scope, subject_type, subject_id, priority ASC, created_at DESC)
  WHERE policy_type = 'agent_action';
