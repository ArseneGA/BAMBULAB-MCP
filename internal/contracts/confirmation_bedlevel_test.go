package contracts

import "testing"

// La mise à niveau du plateau fait partie de la liaison : la désactiver change l'empreinte (la confirmation ne
// vaut plus), mais « activée » (défaut) laisse l'empreinte des anciennes confirmations inchangée.
func TestBindingFingerprintCoversBedLeveling(t *testing.T) {
	b := ConfirmationBinding{PrinterID: "p", ArtifactSHA256: "abc", Plate: 1, Materials: []SlotAssignment{{Slot: "f1>ext", MaterialID: "PLA"}}}
	off := b
	off.NoBedLeveling = true
	if b.Fingerprint() == off.Fingerprint() {
		t.Fatal("désactiver la mise à niveau doit changer l'empreinte approuvée")
	}
	on := b
	on.NoBedLeveling = false
	if b.Fingerprint() != on.Fingerprint() {
		t.Fatal("empreinte instable")
	}
}
