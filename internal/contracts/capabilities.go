package contracts

// Feature décrit une fonction du service et sa disponibilité réelle.
type Feature struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	// Note : limite, raison d'indisponibilité ou état d'avancement.
	Note string `json:"note,omitempty"`
}

// Capabilities est la réponse de get_capabilities / GET /api/capabilities.
type Capabilities struct {
	Service string `json:"service"`
	Version string `json:"version"`
	// Mode d'exécution : "linux_seul" tant qu'aucun agent Windows n'est connecté.
	Mode        string         `json:"mode"`
	Features    []Feature      `json:"features"`
	JobKinds    []string       `json:"job_kinds"`
	Concurrency map[string]int `json:"concurrency" jsonschema:"travaux simultanés par classe (heavy, light)"`
}

// Executable est un programme externe recherché sur la machine du service.
type Executable struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	Found   bool   `json:"found"`
	Path    string `json:"path,omitempty"`
}

// EnvironmentReport est la réponse de inspect_environment. Aucune donnée sensible
// (ni variables d'environnement, ni secrets).
type EnvironmentReport struct {
	ServiceVersion  string       `json:"service_version"`
	GoVersion       string       `json:"go_version"`
	OS              string       `json:"os"`
	Arch            string       `json:"arch"`
	NumCPU          int          `json:"num_cpu"`
	DataDir         string       `json:"data_dir"`
	DataDirWritable bool         `json:"data_dir_writable"`
	SchemaVersion   int          `json:"schema_version"`
	Executables     []Executable `json:"executables"`
	// Missing : éléments manquants qui limitent des fonctions.
	Missing []string `json:"missing"`
}
