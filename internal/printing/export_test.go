package printing

// SetMaxPlanTriangles abaisse le plafond de triangles d'un plan pour les tests ; renvoie la fonction qui le rétablit.
func SetMaxPlanTriangles(n int) func() {
	old := maxPlanTriangle
	maxPlanTriangle = n
	return func() { maxPlanTriangle = old }
}
