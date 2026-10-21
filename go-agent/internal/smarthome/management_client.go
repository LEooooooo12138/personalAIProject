package smarthome

import (
	"context"
	"net/http"
)

func (c *HomeAssistantClient) enableManagedAutomation(ctx context.Context, entity string) error {
	if !validAutomationEntity(entity) {
		return ErrManagementInvalid
	}
	return c.CallService(ctx, "automation", "turn_on", map[string]interface{}{"entity_id": entity})
}
func (c *HomeAssistantClient) disableManagedAutomation(ctx context.Context, entity string, stop bool) error {
	if !validAutomationEntity(entity) {
		return ErrManagementInvalid
	}
	return c.CallService(ctx, "automation", "turn_off", map[string]interface{}{"entity_id": entity, "stop_actions": stop})
}
func (c *HomeAssistantClient) deleteManagedAutomation(ctx context.Context, key string) error {
	if !validAutomationKey(key) {
		return ErrManagementInvalid
	}
	_, err := c.do(ctx, http.MethodDelete, "/api/config/automation/config/"+key, nil)
	return err
}
