package contracts

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// Choix du générateur : github.com/google/jsonschema-go, car c'est le moteur que
// le SDK MCP officiel utilise déjà pour inférer et VALIDER les schémas d'outils
// (même dépendance, mêmes conventions : champs `omitempty` optionnels, balise
// `jsonschema:"description"`, additionalProperties:false). invopop/jsonschema
// aurait ajouté une seconde bibliothèque aux conventions légèrement différentes.

func schemaOptions() *jsonschema.ForOptions {
	return &jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{
			// Charge JSON libre : toute valeur JSON est acceptée. Le schéma vide {} serait
			// sérialisé en booléen `true`, que le validateur du SDK TypeScript (Claude
			// Desktop) refuse dans "properties" : on porte donc une description.
			reflect.TypeFor[json.RawMessage](): {Description: "valeur JSON libre"},
		},
	}
}

// SchemaFor génère le JSON Schema (2020-12) du type T.
func SchemaFor[T any]() (*jsonschema.Schema, error) {
	s, err := jsonschema.For[T](schemaOptions())
	if err != nil {
		return nil, fmt.Errorf("schéma de %T : %w", *new(T), err)
	}
	return s, nil
}

// MustSchemaFor panique si le schéma ne peut être généré (erreur de programmation,
// détectée à l'initialisation).
func MustSchemaFor[T any]() *jsonschema.Schema {
	s, err := SchemaFor[T]()
	if err != nil {
		panic(err)
	}
	return s
}
