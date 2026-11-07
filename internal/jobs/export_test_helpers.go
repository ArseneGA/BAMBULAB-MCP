package jobs

// SetDetailForTest publie un avancement fin sans passer par un handler (tests d'autres paquets).
func SetDetailForTest(id string, d Detail) { details.Store(id, d) }
