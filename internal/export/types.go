// Package export produit l'archive zip partageable d'un projet (une collection ou une liste de modèles) : fichiers
// sources originaux vérifiés par empreinte, sources paramétriques, profils de découpe effectifs, G-code .3mf validés
// (option), README.md et manifest.json. Le format suit l'usage des dépôts MakerWorld / Printables (dossiers files/,
// images/, README) mais RIEN n'est publié automatiquement.
//
// Règles de sécurité : l'archive ne contient ni secret, ni donnée d'imprimante (adresse, numéro de série, code
// d'accès), ni chemin du serveur ; les textes générés passent par un filtre (scrub.go) et les fichiers .3mf sont
// contrôlés avant d'entrer dans l'archive. L'archive est construite en flux par un travail asynchrone, sa taille est
// bornée, et son téléchargement passe par une URL signée à durée limitée (catalog.SignedScopedURL).
package export

import "time"

// Constantes de fonctionnement.
const (
	// JobKind : type de travail de la file.
	JobKind = "project.export"
	// Scope : domaine de signature des URL de téléchargement (distinct de celui des aperçus).
	Scope = "export"
	// Segment : segment d'URL (/api/exports/<id>.zip).
	Segment = "exports"
	// FormatVersion identifie la structure de l'archive (manifest.json).
	FormatVersion = "atelier-bambu-export/1"

	// DefaultMaxBytes borne la taille décompressée de l'archive ; HardMaxBytes est le plafond absolu.
	DefaultMaxBytes int64 = 1 << 30
	HardMaxBytes    int64 = 4 << 30
	// MaxModels borne le nombre de pièces d'un export.
	MaxModels = 200
	// MaxPhotosPerPart borne les photos de fin d'impression retenues par pièce.
	MaxPhotosPerPart = 6
	// DefaultRetention : durée de conservation d'une archive prête sur le serveur.
	DefaultRetention = 24 * time.Hour
	// DefaultLinkTTL : durée de validité par défaut d'une URL de téléchargement signée (FB13 : 10 min étaient trop
	// courtes pour un téléchargement lancé depuis un autre appareil). Réglable (BAMBU_EXPORT_LINK_TTL), toujours
	// entre MinLinkTTL et MaxLinkTTL, et jamais au-delà de la conservation de l'archive.
	DefaultLinkTTL = 2 * time.Hour
	MinLinkTTL     = time.Minute
	MaxLinkTTL     = 24 * time.Hour
	// LinkTTL : alias de DefaultLinkTTL (compatibilité).
	LinkTTL = DefaultLinkTTL
	// MaxKept : nombre maximal d'archives prêtes conservées (les plus anciennes expirent d'abord).
	MaxKept = 20
	// DefaultLicense : valeur du champ licence quand l'utilisateur n'en choisit pas.
	DefaultLicense = "non précisée"
)

// États d'un export.
const (
	StateQueued  = "queued"
	StateReady   = "ready"
	StateFailed  = "failed"
	StateExpired = "expired"
)

// Options de l'export.
type Options struct {
	SkipSubcollections bool              `json:"skip_subcollections,omitempty" jsonschema:"exporter seulement les modèles directement dans la collection (défaut : sous-collections comprises)"`
	Title              string            `json:"title,omitempty" jsonschema:"titre du projet (défaut : nom de la collection ou du modèle)"`
	Description        string            `json:"description,omitempty" jsonschema:"description du projet pour le README (texte libre)"`
	Versions           map[string]string `json:"versions,omitempty" jsonschema:"version à exporter par modèle {model_id: version_id} ; défaut : la dernière version"`
	Quantities         map[string]int    `json:"quantities,omitempty" jsonschema:"quantité à imprimer par modèle {model_id: n} ; défaut 1"`
	IncludeGcode3MF    bool              `json:"include_gcode_3mf,omitempty" jsonschema:"joindre les .gcode.3mf VALIDÉS (désactivé par défaut : fichiers spécifiques à une imprimante et à un réglage)"`
	IncludePhotos      bool              `json:"include_photos,omitempty" jsonschema:"joindre les photos de fin d'impression du journal (métadonnées EXIF retirées des JPEG)"`
	License            string            `json:"license,omitempty" jsonschema:"licence choisie pour le projet (texte libre, ex. CC BY-NC 4.0) ; défaut « non précisée »"`
	AssemblyNotes      string            `json:"assembly_notes,omitempty" jsonschema:"notice d'assemblage (texte libre, Markdown accepté)"`
	Hardware           string            `json:"hardware,omitempty" jsonschema:"quincaillerie nécessaire (texte libre, une ligne par article)"`
	MaxBytes           int64             `json:"max_bytes,omitempty" jsonschema:"taille maximale décompressée de l'archive en octets (défaut 1 Gio, plafond 4 Gio)"`
}

// Input est la demande d'export : une collection OU une liste de modèles.
type Input struct {
	CollectionID string   `json:"collection_id,omitempty" jsonschema:"identifiant, nom ou chemin « A/B » de la collection à exporter"`
	ModelIDs     []string `json:"model_ids,omitempty" jsonschema:"modèles à exporter (alternative à collection_id)"`
	Options      Options  `json:"options,omitempty"`
}

// Skip : élément laissé de côté, avec la raison.
type Skip struct {
	ModelID string `json:"model_id,omitempty"`
	Name    string `json:"name,omitempty"`
	Reason  string `json:"reason"`
}

// PartSummary résume une pièce du plan.
type PartSummary struct {
	ModelID   string `json:"model_id"`
	Name      string `json:"name"`
	Quantity  int    `json:"quantity"`
	VersionID string `json:"version_id,omitempty"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
}

// PlanSummary est ce que l'export contiendra, connu AVANT de lancer le travail.
type PlanSummary struct {
	Title      string        `json:"title"`
	Parts      []PartSummary `json:"parts"`
	Files      int           `json:"files" jsonschema:"fichiers originaux retenus (hors README, manifeste, profils, photos)"`
	TotalBytes int64         `json:"total_bytes" jsonschema:"taille décompressée estimée des fichiers originaux"`
	MaxBytes   int64         `json:"max_bytes"`
	Warnings   []string      `json:"warnings,omitempty"`
	Skipped    []Skip        `json:"skipped,omitempty"`
}

// Summary est le résumé d'une archive terminée.
type Summary struct {
	Parts          int      `json:"parts"`
	Files          int      `json:"files" jsonschema:"entrées de l'archive, README et manifeste compris"`
	IncludesGcode  bool     `json:"includes_gcode_3mf"`
	IncludesPhotos bool     `json:"includes_photos"`
	Licence        string   `json:"license"`
	Warnings       []string `json:"warnings,omitempty"`
	Skipped        []Skip   `json:"skipped,omitempty"`
}

// Record est un export (ligne de project_exports).
type Record struct {
	ID        string    `json:"export_id"`
	JobID     string    `json:"job_id,omitempty"`
	State     string    `json:"state" jsonschema:"queued | ready | failed | expired"`
	Title     string    `json:"title"`
	FileName  string    `json:"file_name,omitempty"`
	SizeBytes int64     `json:"size_bytes,omitempty"`
	SHA256    string    `json:"sha256,omitempty"`
	Summary   *Summary  `json:"summary,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Link est une URL de téléchargement signée, à durée limitée.
type Link struct {
	URL        string    `json:"url" jsonschema:"URL absolue signée (valable sans jeton jusqu'à expires_at)"`
	Path       string    `json:"path" jsonschema:"même lien, relatif à l'adresse du service"`
	Public     bool      `json:"internet_reachable" jsonschema:"true : url passe par l'adresse publique restreinte (joignable hors du réseau privé) ; toujours signée et limitée dans le temps"`
	PrivateURL string    `json:"private_url,omitempty" jsonschema:"même lien sur l'adresse du réseau privé (quand url est l'adresse publique)"`
	ExpiresAt  time.Time `json:"expires_at"`
	FileName   string    `json:"file_name"`
	SizeBytes  int64     `json:"size_bytes"`
}

// Started est la réponse d'un lancement.
type Started struct {
	ExportID string      `json:"export_id"`
	JobID    string      `json:"job_id"`
	Plan     PlanSummary `json:"plan"`
}

// PublishSteps : ce qui reste à faire à la main pour publier (aucune publication automatique).
var PublishSteps = []string{
	"Relire le README.md de l'archive et le compléter à votre goût (description, photos, notice d'assemblage).",
	"Choisir la licence : si elle est « non précisée », l'indiquer avant toute publication ; pour une pièce issue d'un site tiers (URL de provenance dans manifest.json), vérifier que vous avez le droit de la republier.",
	"Créer la page du projet vous-même sur MakerWorld ou Printables, puis y déposer le contenu du dossier files/ (STL, STEP, 3MF), les images de images/ et le texte du README.md. Rien n'est publié automatiquement.",
	"Renseigner dans le formulaire du site la catégorie, les étiquettes, l'image de couverture et les réglages d'impression conseillés : les profils/*.json ne listent que les réglages modifiés par rapport aux préréglages officiels de Bambu Studio.",
	"Pour MakerWorld, préférer joindre un 3MF de projet enregistré depuis Bambu Studio (plateau et réglages inclus) ; l'archive ne remplace pas ce fichier.",
	"Les .gcode.3mf (si inclus, dossier printer-specific/) ne valent que pour une imprimante, une buse et un réglage donnés : ne les publier que si c'est voulu.",
	"Vérifier une dernière fois les photos (arrière-plan, informations personnelles) avant de les mettre en ligne.",
}
