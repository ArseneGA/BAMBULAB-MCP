package printing

import "time"

// Mode compact de get_print_status (FB11). Le suivi complet portait le rapport de validation en entier (contrôles d'archive,
// de compatibilité, adhérence, surplombs, élancement) ET le résumé de découpe (qui répète les mêmes mesures par plaque) : plusieurs
// dizaines de Ko répétés à chaque « ça en est où ? ». Le compact garde ce qui répond à la question : progression, étape, causes
// actives, alertes, plus tout ce qui est nécessaire à la sécurité (état, empreinte, confirmation, options approuvées).

// ValidationBrief : l'essentiel d'un rapport de validation (ce qui bloque, ce qui avertit), sans les contrôles détaillés.
type ValidationBrief struct {
	OK       bool      `json:"ok"`
	At       time.Time `json:"at"`
	Blocking []string  `json:"blocking,omitempty"`
	Warnings []string  `json:"warnings,omitempty"`
	Checks   int       `json:"checks" jsonschema:"nombre de contrôles exécutés (détail avec detail=true)"`
}

// AsCompact rend une copie allégée du suivi ; l'original (le suivi complet, utilisé par la carte et l'API web) n'est pas modifié.
// Champs de sécurité conservés : état, empreinte (artifact_sha256), confirmation_id, plaque, matières, options de démarrage approuvées.
func (st *PrintStatus) AsCompact() *PrintStatus {
	c := *st
	c.Compact = true
	if v := c.Job.Validation; v != nil {
		c.ValidationBrief = &ValidationBrief{OK: v.OK, At: v.At, Blocking: v.Blocking, Warnings: v.Warnings, Checks: len(v.Archive) + len(v.Compat)}
	}
	c.Job.Validation = nil
	c.Job.Summary = nil
	c.Job.Adhesion = nil
	c.Job.Slice = nil
	if pp := c.Job.Print; pp != nil { // déjà rendus plus haut : stage_report, causes, causes_history
		cp := *pp
		cp.Stages, cp.ErrorLog, cp.ErrorDetails, cp.Alerted = nil, nil, nil, nil
		c.Job.Print = &cp
	}
	return &c
}
