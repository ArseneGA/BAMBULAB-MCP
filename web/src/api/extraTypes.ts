/** Types des routes ajoutées au jalon « écrans sans endpoint » : matériaux, expériences passées, mémoire, analyse, sous-parties, agents. */
import type { AnalysisSummary, Dimensions } from './types';
import type { PrintState } from './printTypes';

/* ---- matériaux (internal/engineering, GET /api/materials) ---- */
export interface MaterialSummary {
  id: string; name: string; family: string; vendor: string; tds_source: string; known_properties: number; unknown_properties: string[]; lowest_confidence: string;
}
export interface MaterialList { version: string; note: string; disclaimer: string; materials: MaterialSummary[] }
export interface MaterialProperty { value: number | null; unit: string; tol?: number; source: string; date: string; level: string; domain: string; note?: string }
export interface DataSource { title: string; version: string; url: string; accessed: string; sha256?: string; extract?: string }
export interface MaterialDetail {
  id: string; name: string; family: string; vendor: string; tds_source: string; properties: Record<string, MaterialProperty>; unknown_properties: string[];
  sources: Record<string, DataSource>; db_version: string; note: string; disclaimer: string;
}

/* ---- expériences passées et mémoire des essais ---- */
export interface ExperimentSummary {
  id: string; goal_id?: string; model_id: string; model_version_id: string; created_at: string; state: 'running' | 'complete' | 'partial';
  candidates: number; succeeded: number; failed: number; machine?: string; material_family?: string;
}
export interface MemoryHit {
  result_id: string; experiment_id: string; candidate_id: string; comparability: string; strength: number; outcome: string; observations?: string;
  filament: string; machine: string; candidate_signature?: string; deviations?: Array<{ key: string; planned?: unknown; actual: unknown }>; created_at: string;
}
export interface MemoryResponse { query: { material_family: string; machine: string; filament?: string; model_id?: string; dims_sorted_mm: [number, number, number] }; hits: MemoryHit[]; note: string }
export interface Measurement { criterion?: string; name: string; value: number; unit?: string; note?: string }
export interface ResultInput {
  experiment_id: string; candidate_id: string; outcome: 'success' | 'partial' | 'failed'; observations?: string; measurements?: Measurement[];
  actual_settings?: Record<string, unknown>; filament?: string; machine?: string;
}
export interface PrintResult {
  id: string; experiment_id: string; candidate_id: string; created_at: string; outcome: string; observations?: string; measurements?: Measurement[];
  deviations?: Array<{ key: string; planned?: unknown; actual: unknown }>; context: { material_family: string; filament: string; machine: string }; reuse_conditions: string;
}

/* ---- profil de découpe (POST /api/profiles/modify) ---- */
export interface Override { key: string; value: unknown; target?: string }
export interface ProfileDiff { target: string; key: string; old: unknown; new: unknown; old_source?: string; new_source?: string }
export interface ModifyOutput {
  revision: string; parent_revision?: string; overrides: Override[]; applied: Array<{ target: string; key: string; old: unknown; new: unknown }>; diff: ProfileDiff[]; notes?: string[];
  process: { name: string; provenance: Record<string, string> };
}
export interface ModifyInput { base_revision?: string; base?: { machine?: string; process?: string; filaments?: string[] }; modifications: Override[]; note?: string }

/* ---- sous-parties et analyse ---- */
export interface Part { id: string; kind: 'object' | 'component'; parent_id?: string; label: string; triangles: number; volume_mm3: number; size_mm: Dimensions; glb_node?: string }
export interface PartsOutput { model_id: string; file_id: string; state: 'ok' | 'pending' | 'unavailable'; note?: string; parts: Part[] }
export interface FabReport {
  target: { name: string; bed_x_mm: number; bed_y_mm: number; bed_z_mm: number; nozzle_mm: number }; size_mm: { X: number; Y: number; Z: number };
  fits_bed: boolean; fits_bed_rotated_90_about_z: boolean; margin_mm: { X: number; Y: number; Z: number };
  overhang: { threshold_deg: number; area_mm2: number; fraction_of_surface: number; region_count: number };
  thin_walls: { threshold_mm: number; min_mm: number; p5_mm: number; thin_fraction: number; confidence: string; level: string; note: string };
  orientation?: { level: string; note: string; best_index: number; candidates: Array<{ label: string; height_mm: number; overhang_fraction: number; fits: boolean; score: number }> };
  warnings?: string[];
}
export interface AnalyzeOutput {
  model_id: string; file_id: string; file_name: string; state: string; note?: string; cached?: boolean; unit_applied?: string; summary: AnalysisSummary;
  machine?: string; fabricability?: FabReport; caveats: string[];
}

/* ---- agents Windows ---- */
export interface InventorySummary { files: number; bytes: number; by_state: Record<string, number>; bytes_by_state: Record<string, number>; windows_only_bytes: number }
export interface InventoryItem { root_label: string; rel_path: string; size_bytes: number; sha256: string; state: string; origin_path?: string; source_file_id?: string; note?: string }
export interface AgentTask { id: string; agent_id: string; executor: string; kind: string; state: string; physical: boolean; progress: number; message?: string; created_at?: string; finished_at?: string }
export interface SelectResult { task: AgentTask; files: number; bytes: number }
export type { PrintState };
