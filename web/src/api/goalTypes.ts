/** Types des routes /api/goal-criteria, /api/goals, /api/experiments, /api/variants/apply (internal/goals, jalon 6). */

export interface ParamSpec { values?: string[]; type?: string; required: boolean; description: string }
export interface LeverInfo { id: string; key?: string; kind: string; families: string[]; effect: string; tradeoff: string }
export interface CriterionInfo {
  id: string; label: string; description: string; params: Record<string, ParamSpec>;
  evaluation: { status: 'evaluable' | 'non_evaluable'; by: string[]; metric?: string; indicators?: string[] };
  levers: LeverInfo[] | null; no_levers_reason?: string; test?: string; conflicts_with?: string[];
}
export interface GoalCriteriaCatalog { rules_version: string; note: string; material_families: string[]; family_note: string; criteria: CriterionInfo[] }

export interface CriterionInput {
  id?: string; type: string; text?: string; priority?: number; non_negotiable?: boolean; zone?: string;
  threshold?: { op: string; value: number; unit?: string }; params?: Record<string, unknown>;
}
export interface GoalInput {
  original_text: string; model_id?: string; material?: string; function?: string; finish?: string; constraints?: string[]; criteria: CriterionInput[];
}
export interface GoalCriterion extends CriterionInput {
  id: string; known: boolean; label: string; status: 'evaluable' | 'non_evaluable'; evaluable_by?: string[]; metric?: string; indicators?: string[]; reason?: string; test?: string;
}
export interface Ambiguity { criterion: string; param?: string; question: string; why: string; options?: string[] }
export interface NonEvaluable { criterion: string; label: string; reason: string; test?: string }
export interface GoalConflict { a: string; b: string; type_a: string; type_b: string; why: string; proposals: string[]; needs_priority: boolean; note?: string }
export interface Goal {
  id: string; created_at: string; rules_version: string; original_text: string; model_id?: string; material?: string; material_family?: string;
  criteria: GoalCriterion[]; ambiguities: Ambiguity[]; non_evaluable: NonEvaluable[]; conflicts: GoalConflict[]; warnings?: string[];
}

export interface BaseSpec { machine?: string; printer?: string; nozzle?: number; process?: string; filaments?: string[] }
export interface CreateExperimentInput {
  model_id: string; model_version_id?: string; expected_model_revision?: string; goal_id: string; base: BaseSpec; base_profile_revision?: string; candidates?: number;
}
export interface Metric { value: number; unit: string; label: string; note?: string }
export interface CandidateChange { criterion: string; criterion_type: string; lever: string; key?: string; target?: string; from?: unknown; to?: unknown; effect: string; expected_tradeoff?: string }
export interface ExpCandidate {
  id: string; index: number; kind: 'reference' | 'variant'; label: string; rationale: string; overrides: Array<{ key: string; value: unknown; target?: string }>;
  changes: CandidateChange[]; factors: number; expected_tradeoffs: string[]; notes?: string[];
  state: 'pending' | 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'; job_id?: string; error?: string; metrics?: { values: Record<string, Metric>; support_used: boolean };
}
export interface Experiment {
  id: string; goal_id?: string; model_id: string; model_version_id: string; created_at: string;
  details: { base: { machine: string; process: string; filaments: string[]; material_family?: string }; requested_candidates: number; skipped_levers: Array<{ criterion: string; lever: string; reason: string }>; notes: string[] | null };
  candidates: ExpCandidate[];
}

export interface EngEntry {
  criterion: string; candidate_id: string; label: string; method: string; safety_factor: number; verdict: string; stress_MPa: number; deflection_mm: number;
  confidence_level: string; assumptions: string[]; warnings?: string[]; note?: string; error?: string; orientation_assumed: string;
}
export interface ThresholdCheck { criterion: string; label: string; metric: string; op: string; limit: number; value: number; pass: boolean; non_negotiable?: boolean; basis: string }
export interface CompareCandidate {
  id: string; label: string; kind: string; state: string; rationale: string; changes: string[]; orientation?: string; expected_tradeoffs: string[]; notes?: string[];
  metrics?: Record<string, Metric>; engineering?: EngEntry[]; threshold_checks?: ThresholdCheck[]; violates_non_negotiable?: boolean; error?: string;
}
export interface Ranking { criterion: string; label: string; metric: string; better: 'lower' | 'higher'; order: Array<{ candidate_id: string; value: number }>; basis: string; all_tied?: boolean; note?: string }
export interface CompareOutput {
  experiment_id: string; model_version_id: string; goal_id?: string; complete: boolean; pending?: string[]; candidates: CompareCandidate[]; rankings: Ranking[];
  not_evaluable: NonEvaluable[]; conflicts?: GoalConflict[]; memory?: unknown[]; skipped_levers?: Array<{ criterion: string; lever: string; reason: string }>; notice: string;
}
export interface ApplyOutput {
  profile_revision: string; model_id: string; model_version_id: string; experiment_id: string; candidate_id: string; label: string;
  overrides: Array<{ key: string; value: unknown; target?: string }>; notes: string[]; applied_at: string;
}
