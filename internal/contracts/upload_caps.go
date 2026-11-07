package contracts

// UploadInfo : envoi de fichiers, dossiers d'import et import par lien (get_capabilities, /api/capabilities).
type UploadInfo struct {
	ImportRoots    []string       `json:"import_roots" jsonschema:"dossiers d'import autorisés du serveur (import_files / scan_folder y lisent) ; vide = import par chemin refusé"`
	MaxUploadBytes int64          `json:"max_upload_bytes" jsonschema:"taille maximale d'un fichier envoyé (upload_file, glisser-déposer)"`
	MaxByFormatMB  map[string]int `json:"max_upload_mb_by_format" jsonschema:"taille maximale par format, en Mo"`
	MaxChunkChars  int            `json:"max_chunk_chars" jsonschema:"taille maximale d'un morceau base64 par appel upload_file"`
	Formats        []string       `json:"formats" jsonschema:"formats acceptés (stl, obj, 3mf, step)"`
	TTLSeconds     int            `json:"upload_ttl_seconds" jsonschema:"inactivité au bout de laquelle une session d'envoi est purgée"`
	URLImport      URLImportInfo  `json:"url_import"`
}

// URLImportInfo : état de l'import par lien.
type URLImportInfo struct {
	Enabled        bool     `json:"enabled"`
	AllowedDomains []string `json:"allowed_domains" jsonschema:"domaines (et sous-domaines) dont import_from_url accepte les liens et les redirections"`
	MaxBytes       int64    `json:"max_bytes"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	Note           string   `json:"note,omitempty"`
}
