package smarthome

// ControlCatalog supplies the existing directory service to instant control wiring.
func (m *Manager) ControlCatalog() ControlCatalogProvider { return m.catalog }
