/** Types des routes /api/printer-models, /api/printers, /api/print-jobs, /api/confirmations, /api/agents (internal/printing, internal/contracts). */

export interface PrinterModel {
  id: string; name: string; vendor: string; build_volume?: { x: number; y: number; z: number }; nozzle_diameters_mm?: number[]; plates?: string[];
  enclosed?: boolean; heated_bed?: boolean; max_bed_temp_c?: number; max_nozzle_temp_c?: number; source?: string;
}
export interface PrinterView {
  id: string; model_id: string; model_name: string; name: string; host: string; serial: string; firmware?: string; nozzle_mm: number; ams_units: number;
  access_code_set: boolean; setup_url?: string; pinned_sha256?: string; insecure_reason?: string; allow_start_from_finish?: boolean; created_at: string;
}
export interface AddPrinterInput { name: string; model_id: string; host: string; serial: string; nozzle_mm?: number; ams_units?: number }
export interface PrinterStatus {
  printer_id: string; reachable: boolean; error?: string; state: string; gcode_state?: string; progress_percent: number; layer: number; total_layers: number;
  remaining_minutes: number; temps: { nozzle: number; nozzle_target: number; bed: number; bed_target: number; chamber: number }; print_error: number; hms_count: number;
  fail_reason?: string; current_file?: string; firmware?: string; updated_at?: string;
  hms_codes?: string[]; display?: string; explanation?: string;
  last_job?: { print_job_id: string; name: string; state: PrintState; stopped_by_user?: boolean; at: string };
}
export type HistoryView = 'all' | 'active' | 'finished' | 'failed' | 'archived' | 'trash';
export interface JobProgress { job_id: string; state: string; percent: number; phase?: string; message?: string; bytes_done?: number; bytes_total?: number; eta_seconds?: number; queue_position?: number; running_ahead?: number; stalled?: boolean; estimated?: boolean }
export interface ModelGroup { model_id: string; model_name: string; count: number; failed: number; jobs: PrintJob[] }
export interface HistoryReply { print_jobs: PrintJob[]; view: HistoryView; counts: Record<string, number>; groups?: ModelGroup[]; progress?: Record<string, JobProgress> }

export type PrintState = 'DRAFT' | 'PREPARED' | 'SLICING' | 'VALIDATED' | 'UPLOADING' | 'UPLOADED' | 'AWAITING_PRINT_CONFIRMATION' | 'START_REQUESTED' | 'PRINTING' | 'COMPLETED' | 'FAILED' | 'CANCELLED' | 'START_UNKNOWN';
export interface FilamentAssignment { filament_slot: number; tray: string; material_id: string }
export interface Check { name: string; ok: boolean; level: 'error' | 'warn'; detail?: string }
export interface ValidationReport { ok: boolean; at: string; printer_id: string; plate: number; archive_checks: Check[]; compatibility_checks: Check[]; blocking?: string[]; warnings?: string[]; validator: string }
export interface PrintJob {
  id: string; state: PrintState; printer_id: string; model_id?: string; version_id?: string; name?: string; artifact_sha256?: string; plate?: number;
  assignments?: FilamentAssignment[]; use_ams: boolean; summary?: { printer_model?: string; machine_preset?: string; process?: string; filament_presets?: string[]; nozzles?: string[]; bed_type?: string };
  validation?: ValidationReport; confirmation_id?: string; print?: { percent: number; layer: number; total_layers: number; remaining_minutes: number; printer_state: string; observed_at: string };
  archived?: boolean; trashed?: boolean; stopped_by_user?: boolean;
  last_error?: string; events?: Array<{ at: string; event: string; from: string; to: string; note?: string }>; created_at: string; updated_at: string; next?: string;
}
export interface PromptDetails { file: string; printer_name: string; printer_model: string; sha256: string; plate: number; materials: FilamentAssignment[]; estimated_minutes?: number; estimated_grams?: number; alerts?: string[] }
/** Températures et origine du G-code lues dans l'archive (internal/printing/safety_view.go). */
export interface SafetyView {
  analysed: boolean; materials?: string[]; nozzle_start_max_c: number; nozzle_first_layer_c: number; nozzle_print_min_c: number; nozzle_print_max_c: number;
  bed_first_layer_c: number; bed_min_c: number; bed_max_c: number; chamber_max_c?: number; gcode_profile_line: string; gcode_official: boolean; anomalies?: string[];
}
export interface UploadOutput { print_job_id: string; state: PrintState; done: boolean; replayed: boolean; question?: string; details?: PromptDetails; note: string; job: PrintJob }
export interface ConfirmationView {
  confirmation_id: string; print_job_id: string; expires_at: string; confirm_url: string; approved: boolean; binding: PromptDetails; safety?: SafetyView | null; instructions: string; state: PrintState;
}
export interface PrintStatus { job: PrintJob; printer?: PrinterStatus; note?: string }
export interface StartOutput { print_job_id: string; state: PrintState; outcome: string; replayed: boolean; note: string; job: PrintJob }
export interface AgentStatus {
  id: string; name: string; online: boolean; last_seen?: string; agent_version?: string; studio_installed: boolean; studio_running: boolean; studio_version?: string;
  local_slicer: boolean; shared_folders?: string[]; cad?: string[]; tasks?: string[];
}
export interface ReconcileOutput { print_job_id: string; outcome: 'started' | 'not_started' | 'unknown'; reason: string; state: PrintState; note: string; job: PrintJob }
export interface CancelOutput { print_job_id: string; outcome: 'cancelled' | 'stop_requested' | 'stopped' | 'refused'; state: PrintState; stop_url?: string; stop_expires_at?: string; question?: string; note: string; job: PrintJob }
