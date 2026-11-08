package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// Les options de démarrage (flow_cali, vibration_cali, timelapse) font partie de la liaison approuvée : chacune
// change l'empreinte consommée par ConsumeConfirmation ; leurs valeurs par défaut laissent l'empreinte INCHANGÉE
// (les confirmations émises avant cette version restent valables, rien à migrer).
func TestBindingFingerprintCoversStartOptions(t *testing.T) {
	b := ConfirmationBinding{PrinterID: "p", ArtifactSHA256: "abc", Plate: 1, Materials: []SlotAssignment{{Slot: "f1>ext", MaterialID: "PLA"}}}
	base := b.Fingerprint()
	for name, mod := range map[string]func(*ConfirmationBinding){
		"sans calibration du débit":       func(x *ConfirmationBinding) { x.NoFlowCali = true },
		"sans calibration des vibrations": func(x *ConfirmationBinding) { x.NoVibrationCali = true },
		"timelapse":                       func(x *ConfirmationBinding) { x.Timelapse = true },
	} {
		c := b
		mod(&c)
		if c.Fingerprint() == base {
			t.Errorf("%s : l'empreinte doit changer", name)
		}
	}
}

// Empreinte des liaisons « par défaut » identique à celle calculée avec l'ancienne structure (avant les options).
func TestBindingDefaultFingerprintUnchangedFromLegacyShape(t *testing.T) {
	type legacy struct {
		PrinterID      string           `json:"printer_id"`
		ArtifactSHA256 string           `json:"artifact_sha256"`
		Plate          int              `json:"plate"`
		Materials      []SlotAssignment `json:"materials"`
		NoBedLeveling  bool             `json:"no_bed_leveling,omitempty"`
	}
	mats := []SlotAssignment{{Slot: "f1>ams1:2", MaterialID: "PLA Basic noir"}}
	raw, _ := json.Marshal(legacy{PrinterID: "prn_1", ArtifactSHA256: "deadbeef", Plate: 1, Materials: mats})
	sum := sha256.Sum256(raw)
	want := hex.EncodeToString(sum[:])
	got := ConfirmationBinding{PrinterID: "prn_1", ArtifactSHA256: "deadbeef", Plate: 1, Materials: mats}.Fingerprint()
	if got != want {
		t.Fatalf("l'empreinte par défaut a changé : %s, attendue %s", got, want)
	}
	off := legacy{PrinterID: "prn_1", ArtifactSHA256: "deadbeef", Plate: 1, Materials: mats, NoBedLeveling: true}
	raw, _ = json.Marshal(off)
	sum = sha256.Sum256(raw)
	if g := (ConfirmationBinding{PrinterID: "prn_1", ArtifactSHA256: "deadbeef", Plate: 1, Materials: mats, NoBedLeveling: true}).Fingerprint(); g != hex.EncodeToString(sum[:]) {
		t.Fatal("l'empreinte « sans mise à niveau » a changé")
	}
}
