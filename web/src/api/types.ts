/**
 * Types de l'API HTTP de bambud (jalons 1 et 2). Ils reflètent les structures JSON
 * de internal/catalog et internal/contracts ; un champ optionnel côté Go est
 * optionnel ici, et une valeur inconnue n'est jamais remplacée par un défaut.
 */

export type FileFormat = 'stl' | 'obj' | 'step' | '3mf';
export type Availability = 'linux' | 'windows_only';
export type PreviewState = 'ready' | 'pending' | 'none' | 'windows_only' | 'backend_missing' | 'error';

export interface Dimensions { x: number; y: number; z: number }

export interface PreviewRef { state: PreviewState; sha256?: string; url?: string; note?: string }

export interface ModelSummary {
  id: string;
  name: string;
  collection_id?: string;
  collection_path?: string;
  tags: string[];
  dimensions?: Dimensions;
  formats: string[];
  file_count: number;
  size_bytes: number;
  availability: Availability;
  versions: number;
  license?: string;
  preview: PreviewRef;
  trashed?: boolean;
  created_at: string;
  updated_at: string;
}

export interface SearchResult {
  models: ModelSummary[];
  total: number;
  limit: number;
  offset: number;
  has_more: boolean;
  next_offset?: number;
}

export interface SearchParams {
  q?: string;
  tag?: string[];
  collection?: string;
  format?: string;
  availability?: string;
  trashed?: 'include' | 'only';
  sort?: 'name' | 'created' | 'updated';
  desc?: boolean;
  limit?: number;
  offset?: number;
}

export interface CollectionInfo {
  id: string;
  name: string;
  path: string;
  parent_id?: string;
  models: number;
  total_models: number;
  created_at: string;
}

export interface Provenance { source?: string; url?: string; author?: string; origin_path?: string; imported_at?: string; note?: string }

export interface ModelRecord {
  id: string;
  collection_id?: string;
  name: string;
  tags?: string[];
  dimensions?: Dimensions;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
  description?: string;
  license?: string;
  provenance?: Provenance;
}

export interface SourceFile {
  id: string;
  model_id: string;
  name: string;
  format: FileFormat;
  size_bytes: number;
  sha256: string;
  availability: Availability;
  origin_path?: string;
  created_at: string;
  rel_path?: string;
  origin_mtime?: string;
  unit?: string;
}

export interface ModelVersion {
  id: string;
  model_id: string;
  parent_id?: string;
  label: string;
  note?: string;
  source_file_ids: string[];
  created_at: string;
}

export interface AnalysisSummary {
  state: 'ok' | 'pending' | 'error' | 'backend_missing' | 'none';
  note?: string;
  format?: string;
  unit?: string;
  unit_assumed?: boolean;
  unit_note?: string;
  objects?: number;
  triangles?: number;
  size_mm?: Dimensions;
  volume_mm3?: number;
  area_mm2?: number;
  watertight?: boolean;
  open_edges?: number;
  non_manifold_edges?: number;
  degenerate_triangles?: number;
  components?: number;
  warnings?: string[];
  not_interpreted?: string[];
  level?: string;
}

export interface AuditLine { at: string; actor: string; operation: string; detail?: unknown }

export interface ModelDetails {
  model: ModelRecord;
  collection_path?: string;
  files: SourceFile[];
  versions: ModelVersion[];
  current_file_id?: string;
  preview: PreviewRef;
  analysis: AnalysisSummary;
  cad?: { state: string; note?: string };
  same_content_models?: string[];
  audit: AuditLine[];
}

/* ---- import / scan ---- */
export interface ScanFile { path: string; rel_path: string; format: string; size_bytes: number; mtime: string; state: 'new' | 'changed' | 'imported' }
export interface ScanReport {
  path: string; root: string; recursive: boolean; read_only: boolean;
  total_files: number; total_bytes: number;
  by_format: Record<string, { count: number; bytes: number }>;
  unsupported_files: number; unsupported_extensions?: Record<string, number>;
  new_files: number; changed_files: number; already_imported: number;
  bytes_to_copy: number; free_disk_bytes?: number; enough_space?: boolean;
  largest?: ScanFile[]; files: ScanFile[]; files_truncated?: boolean; walk_truncated?: boolean;
  skipped?: string[]; notes?: string[];
}

export interface ImportInput {
  paths: string[];
  collection?: string;
  copy?: boolean;
  on_duplicate?: 'skip' | 'keep';
  recursive?: boolean;
  keep_structure?: boolean;
  tags?: string[];
  license?: string;
  source?: string;
  unit?: string;
  wait_seconds?: number;
}

export interface FileResult { path: string; rel_path?: string; status: string; format?: string; size_bytes?: number; model_id?: string; duplicate_of_model_id?: string; note?: string }
export interface ImportReport { counts: Record<string, number>; bytes_copied: number; total: number; files: FileResult[]; files_truncated?: boolean; skipped?: string[] }
export interface ImportStarted { job_id: string; state: string; done: boolean; report?: ImportReport; error?: string; note: string }

/* ---- travaux ---- */
export type JobState = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'unknown';
export interface Job {
  id: string; kind: string; class: string; state: JobState; progress: number; message?: string; logs?: string[];
  result?: unknown; error?: { code: string; message: string }; cancel_requested?: boolean;
  created_at: string; started_at?: string; finished_at?: string;
}
export const jobTerminal = (s: JobState) => s !== 'queued' && s !== 'running';

/* ---- opérations ---- */
export type OpName = 'rename' | 'rename_file' | 'move' | 'add_tags' | 'remove_tags' | 'set_license' | 'delete';
export interface Operation {
  op: OpName; model_ids?: string[]; file_id?: string; new_name?: string; collection?: string; to_root?: boolean; tags?: string[]; license?: string;
}
export interface Change { op: string; target_type: 'model' | 'file'; target_id: string; target_name: string; field: string; before: string; after: string }
export interface OpPreview {
  token: string; expires_at: string; operations: number; models_affected: number; files_affected: number;
  changes: Change[]; creates_collections?: string[]; to_trash: number; warnings?: string[]; note: string;
}
export interface ApplyResult { applied: number; changes: Change[]; trashed?: string[]; collections_created?: string[]; note: string }
export interface RestoreResult { restored: Array<{ id: string; name: string; tags?: string[] }>; skipped?: string[] }

/* ---- capacités ---- */
export interface Feature { id: string; available: boolean; note?: string }
export interface Capabilities { service: string; version: string; mode: string; features: Feature[]; agents?: Array<{ id: string; name: string; online: boolean; last_seen?: string; studio_installed: boolean; studio_running: boolean; local_slicer: boolean }>; job_kinds: string[]; concurrency: Record<string, number> }
export interface Health { status: string; version: string; schema_version: number }

/* ---- doublons ---- */
export interface DupFile { file_id: string; name: string; model_id: string; model_name: string; collection_path?: string; availability: string; origin_path?: string; trashed?: boolean }
export interface DupGroup { sha256: string; size_bytes: number; copies: number; files: DupFile[]; logical_wasted_bytes: number }
export interface DuplicatesResult { groups: DupGroup[]; total_groups: number; note: string }
