package filament

// Publish diffuse un évènement aux abonnés (tests des consommateurs : notifications).
func (s *Service) Publish(e Event) { s.publish(e) }
