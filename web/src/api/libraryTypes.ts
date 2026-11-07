/** Types de la bibliothèque de gabarits et de la calibration (internal/library, internal/calibration). */

export interface ParamSpec {
  name: string;
  type: 'number' | 'integer' | 'enum' | 'boolean';
  unit?: string;
  default: number | string | boolean;
  min?: number;
  max?: number;
  options?: string[];
  description: string;
  auto?: string;
}

export interface TemplateInfo {
  id: string;
  kind: 'builtin' | 'variant';
  version: string;
  schema_hash: string;
  title: string;
  category: string;
  summary: string;
  description: string;
  tags?: string[];
  params: ParamSpec[];
  base_template?: string;
  fixed_params?: Record<string, unknown>;
  thumbnail_url: string;
}

export interface TemplateList { templates: TemplateInfo[]; categories: string[]; note: string }

export interface CreateInput {
  template: string;
  params?: Record<string, unknown>;
  name?: string;
  material?: string;
  nozzle_diameter?: number;
  printer?: string;
  filament?: string;
  compensate_holes?: boolean;
}

export interface AutoParam { param: string; rule: string; nominal?: unknown; value: number; unit: string; level: string; source: string }
export interface EngResult { value: number; unit: string; level: string; assumptions: string[]; warnings?: string[]; validity: string }
export interface Calc { name: string; param?: string; result: EngResult; extra?: Record<string, unknown> }
export interface CalibrationUsed { applied: boolean; provenance: string; matched?: string[]; note: string }

export interface CreateOutput {
  model_id: string;
  version_id: string;
  template: string;
  template_version: string;
  variant?: string;
  params: Record<string, unknown>;
  requested: Record<string, unknown>;
  auto: AutoParam[];
  calcs: Calc[];
  calibration: CalibrationUsed;
  parts: Array<{ name: string; volume_mm3: number; solids: number; watertight: boolean; triangles: number }>;
  assumptions: string[];
  warnings: string[];
  bbox_mm?: { size?: number[] };
  derived?: Record<string, unknown>;
  thumbnail_url?: string;
  note: string;
}

export interface SaveOutput { variant: TemplateInfo; note: string }

export interface CalibrationKey { printer?: string; nozzle_mm: number; material: string; filament?: string }

export interface CalibrationPiece {
  kind: string;
  name: string;
  model_id: string;
  version_id: string;
  description: string;
  measure: string[];
  nozzle_temperature_c?: number;
  profile_revision?: string;
  warnings?: string[];
}

export interface GenerateOutput {
  set_id: string;
  key: CalibrationKey;
  pieces: CalibrationPiece[];
  measures_template: Record<string, unknown>;
  instructions: string[];
  note: string;
}

export interface DiameterMeasure { nominal: number; measured: number; axis?: 'vertical' | 'horizontal' }

export interface Measures {
  fit_slides_gap_mm?: number;
  fit_snug_gap_mm?: number;
  holes?: DiameterMeasure[];
  shafts?: DiameterMeasure[];
  cube_mm?: number[];
  best_temp_c?: number;
  bridge_max_span_mm?: number;
  overhang_max_deg?: number;
}

export interface CalibrationDoc {
  provenance: string;
  calibrated_at: string;
  slides_gap_mm?: number;
  snug_gap_mm?: number;
  hole_vertical?: { offset_mm: number; slope: number; n: number; max_residual_mm: number };
  hole_horizontal?: { offset_mm: number; slope: number; n: number; max_residual_mm: number };
  shaft?: { offset_mm: number; slope: number; n: number; max_residual_mm: number };
  shrink_scale_xy?: number;
  shrink_scale_z?: number;
  best_temp_c?: number;
  bridge_max_span_mm?: number;
  overhang_max_deg?: number;
}

export interface RecordOutput { record_id: string; key: CalibrationKey; results: CalibrationDoc; active: CalibrationDoc; explanation: string[]; effects: string[]; note: string }

export interface CalibrationLookup {
  resolution: { calibration?: CalibrationDoc; matched: string[]; note: string };
  history: Array<{ record_id: string; key: CalibrationKey; results: CalibrationDoc; notes?: string; created_at: string }>;
}
