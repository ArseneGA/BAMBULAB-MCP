// Package contracts définit les types partagés du service (catalogue, travaux,
// imprimantes, impressions, matières, objectifs), leurs étiquettes JSON, la
// machine d'états d'impression et le mécanisme de confirmation.
//
// Convention : un champ optionnel est un pointeur ou porte `omitempty` ; une
// valeur inconnue est toujours nil, jamais une valeur par défaut inventée.
package contracts

import "time"

// FileFormat est le format d'un fichier source.
type FileFormat string

const (
	FormatSTL  FileFormat = "stl"
	FormatOBJ  FileFormat = "obj"
	FormatSTEP FileFormat = "step"
	Format3MF  FileFormat = "3mf"
)

// Valid indique si le format est pris en charge.
func (f FileFormat) Valid() bool {
	switch f {
	case FormatSTL, FormatOBJ, FormatSTEP, Format3MF:
		return true
	}
	return false
}

// Availability dit où un fichier est réellement disponible (dossier §15.4).
type Availability string

const (
	// AvailableLinux : copié et vérifié sur le stockage du service.
	AvailableLinux Availability = "linux"
	// AvailableWindowsOnly : indexé mais pas encore copié ; hors des capacités hors ligne.
	AvailableWindowsOnly Availability = "windows_only"
)

// Valid indique si la disponibilité est connue.
func (a Availability) Valid() bool { return a == AvailableLinux || a == AvailableWindowsOnly }

// Collection regroupe des modèles.
type Collection struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ParentID  *string   `json:"parent_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Dimensions en millimètres.
type Dimensions struct {
	X float64 `json:"x" jsonschema:"mm"`
	Y float64 `json:"y" jsonschema:"mm"`
	Z float64 `json:"z" jsonschema:"mm"`
}

// Model est un modèle logique : un nom n'est pas une géométrie (dossier §14.2).
type Model struct {
	ID           string      `json:"id"`
	CollectionID *string     `json:"collection_id,omitempty"`
	Name         string      `json:"name"`
	Tags         []string    `json:"tags,omitempty"`
	Dimensions   *Dimensions `json:"dimensions,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
	// DeletedAt non nil = en corbeille (restaurable).
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// SourceFile est un fichier original, immuable, adressé par son empreinte.
type SourceFile struct {
	ID           string       `json:"id"`
	ModelID      string       `json:"model_id"`
	Name         string       `json:"name"`
	Format       FileFormat   `json:"format"`
	SizeBytes    int64        `json:"size_bytes"`
	SHA256       string       `json:"sha256"`
	Availability Availability `json:"availability"`
	// OriginPath : chemin d'origine côté Windows, informatif (jamais utilisé pour lire).
	OriginPath *string   `json:"origin_path,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// ModelVersion est une version immuable d'un modèle.
type ModelVersion struct {
	ID            string    `json:"id"`
	ModelID       string    `json:"model_id"`
	ParentID      *string   `json:"parent_id,omitempty"`
	Label         string    `json:"label"`
	Note          string    `json:"note,omitempty"`
	SourceFileIDs []string  `json:"source_file_ids"`
	CreatedAt     time.Time `json:"created_at"`
}
