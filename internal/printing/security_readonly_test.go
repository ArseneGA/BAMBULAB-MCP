package printing_test

import "testing"

// Point 11 : get_print_status relit l'imprimante et met l'impression à jour : pas de ReadOnlyHint.
func TestGetPrintStatusIsNotAdvertisedReadOnly(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	tools, err := r.cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tl := range tools.Tools {
		if tl.Name == "get_print_status" {
			found = true
			if tl.Annotations != nil && tl.Annotations.ReadOnlyHint {
				t.Fatal("get_print_status ne doit pas porter ReadOnlyHint")
			}
		}
	}
	if !found {
		t.Fatal("outil absent")
	}
}
