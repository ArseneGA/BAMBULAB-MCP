package agentlink

// CheckContentForTest expose le contrôle de contenu aux tests externes.
func CheckContentForTest(path, name string) error { return checkContent(path, fileExt(name)) }
