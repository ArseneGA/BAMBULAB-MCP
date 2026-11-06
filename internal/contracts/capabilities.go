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
	// Agents : agents Windows appairés et leur disponibilité (jalon 8). Vide sans agent.
	Agents []AgentStatus `json:"agents,omitempty"`
	// Studio : Bambu Studio du serveur et profils réellement chargés (vérifiable sans lancer de découpe).
	Studio *StudioInfo `json:"studio,omitempty"`
}

// StudioInfo décrit l'installation de Bambu Studio utilisée par le service et les profils chargés.
type StudioInfo struct {
	Available   bool   `json:"available"`
	Path        string `json:"path,omitempty"`
	Version     string `json:"version,omitempty" jsonschema:"version détectée de Bambu Studio (ex. 02.09.00.00)"`
	ProfilesDir string `json:"profiles_dir,omitempty" jsonschema:"dossier des profils système BBL"`
	// Profils chargés ; nil tant que le chargement (lancé au premier appel) n'est pas terminé.
	Profiles *ProfileCounts `json:"profiles,omitempty"`
	Note     string         `json:"note,omitempty"`
}

// ProfileCounts : nombre de profils chargés par famille (presets instanciables pour machines/process/filaments).
type ProfileCounts struct {
	Models    int `json:"models" jsonschema:"modèles d'imprimante"`
	Machines  int `json:"machines"`
	Processes int `json:"processes"`
	Filaments int `json:"filaments"`
}

// AgentStatus est la vue publique d'un agent Windows appairé (jamais de secret).
type AgentStatus struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Online   bool   `json:"online" jsonschema:"vrai si l'agent a donné signe de vie récemment"`
	LastSeen string `json:"last_seen,omitempty" jsonschema:"dernière vue (RFC 3339)"`
	Version  string `json:"agent_version,omitempty"`
	// Bambu Studio sur le poste Windows.
	StudioInstalled bool     `json:"studio_installed"`
	StudioRunning   bool     `json:"studio_running"`
	StudioVersion   string   `json:"studio_version,omitempty"`
	LocalSlicer     bool     `json:"local_slicer"`
	SharedFolders   []string `json:"shared_folders,omitempty" jsonschema:"étiquettes des dossiers autorisés à l'import"`
	CAD             []string `json:"cad,omitempty"`
	Tasks           []string `json:"tasks,omitempty" jsonschema:"types de travaux acceptés"`
}

// Executable est un programme externe recherché sur la machine du service.
type Executable struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	Found   bool   `json:"found"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
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
