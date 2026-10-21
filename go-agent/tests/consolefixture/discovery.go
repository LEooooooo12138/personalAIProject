package main

import (
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

// Only a synthetic snapshot is seeded. No events or recorder completeness are
// invented: the local history endpoint deliberately returns an empty result.
func prepareFixtureDiscovery(app *core.App) error {
	return app.SmartHome.GetStore().SaveSnapshot([]smarthome.EntityState{{EntityID: "switch.channel_1", State: "off"}})
}
