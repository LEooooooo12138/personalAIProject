package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"strings"
	"time"
)

// Registry types intentionally omit connectivity, config entry and timestamp fields.
type RegistryArea struct {
	ID      string   `json:"area_id"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}
type RegistryDevice struct {
	ID         string   `json:"id"`
	Name       *string  `json:"name"`
	NameByUser *string  `json:"name_by_user"`
	AreaID     *string  `json:"area_id"`
	Labels     []string `json:"labels"`
}
type RegistryEntity struct {
	EntityID     string   `json:"entity_id"`
	Name         *string  `json:"name"`
	OriginalName *string  `json:"original_name"`
	DeviceID     *string  `json:"device_id"`
	AreaID       *string  `json:"area_id"`
	Labels       []string `json:"labels"`
	DisabledBy   *string  `json:"disabled_by"`
	HiddenBy     *string  `json:"hidden_by"`
	Category     *string  `json:"entity_category"`
}
type RegistryLabel struct {
	ID   string `json:"label_id"`
	Name string `json:"name"`
}
type RegistrySnapshot struct {
	Areas    []RegistryArea   `json:"areas"`
	Devices  []RegistryDevice `json:"devices"`
	Entities []RegistryEntity `json:"entities"`
	Labels   []RegistryLabel  `json:"labels"`
}

var ErrRegistryUnavailable = errors.New("home assistant registry unavailable")

type registrySession struct {
	conn *websocket.Conn
	id   int
	ctx  context.Context
}

func (c *HomeAssistantClient) registrySession(ctx context.Context) (*registrySession, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, closeConn, err := c.openAuthenticatedWS(ctx)
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	cleanup := func() { closeConn(); cancel() }
	deadline, _ := ctx.Deadline()
	conn.SetReadDeadline(deadline)
	conn.SetWriteDeadline(deadline)
	return &registrySession{conn: conn, ctx: ctx}, cleanup, nil
}
func (s *registrySession) request(typ string, fields map[string]any, out any) error {
	s.id++
	req := map[string]any{"id": s.id, "type": typ}
	for k, v := range fields {
		req[k] = v
	}
	if e := s.conn.WriteJSON(req); e != nil {
		return s.failure(e)
	}
	var response struct {
		ID      int             `json:"id"`
		Type    string          `json:"type"`
		Success *bool           `json:"success"`
		Result  json.RawMessage `json:"result"`
		Error   *HAError        `json:"error"`
	}
	if e := s.conn.ReadJSON(&response); e != nil {
		return s.failure(e)
	}
	if response.ID == s.id && response.Type == "result" && response.Success != nil && !*response.Success && response.Error != nil {
		switch response.Error.Code {
		case "unauthorized", "not_allowed":
			return newHAError("ha_forbidden")
		case "auth_invalid":
			return newHAError("ha_auth_required")
		}
	}
	if response.ID != s.id || response.Type != "result" || response.Success == nil || !*response.Success || len(response.Result) == 0 || string(response.Result) == "null" {
		return registryProtocolError()
	}
	if e := json.Unmarshal(response.Result, out); e != nil {
		return registryProtocolError()
	}
	return nil
}
func (s *registrySession) failure(cause error) error {
	if e := s.ctx.Err(); e != nil {
		return e
	}
	var syntax *json.SyntaxError
	var mismatch *json.UnmarshalTypeError
	if errors.As(cause, &syntax) || errors.As(cause, &mismatch) {
		return registryProtocolError()
	}
	return safeHAError(cause)
}
func registryProtocolError() error {
	return errors.Join(ErrRegistryUnavailable, newHAError("ha_invalid_response"))
}
func (c *HomeAssistantClient) GetRegistry(ctx context.Context) (RegistrySnapshot, error) {
	var reg RegistrySnapshot
	s, close, e := c.registrySession(ctx)
	if e != nil {
		return reg, e
	}
	defer close()
	for _, item := range []struct {
		typ string
		out any
	}{
		{"config/area_registry/list", &reg.Areas}, {"config/device_registry/list", &reg.Devices},
		{"config/entity_registry/list", &reg.Entities}, {"config/label_registry/list", &reg.Labels},
	} {
		if e = s.request(item.typ, nil, item.out); e != nil {
			return RegistrySnapshot{}, e
		}
	}
	if reg.Areas == nil || reg.Devices == nil || reg.Entities == nil || reg.Labels == nil {
		return RegistrySnapshot{}, registryProtocolError()
	}
	for _, area := range reg.Areas {
		if area.ID == "" {
			return RegistrySnapshot{}, registryProtocolError()
		}
	}
	for _, device := range reg.Devices {
		if device.ID == "" {
			return RegistrySnapshot{}, registryProtocolError()
		}
	}
	for _, entity := range reg.Entities {
		if entity.EntityID == "" {
			return RegistrySnapshot{}, registryProtocolError()
		}
	}
	for _, label := range reg.Labels {
		if label.ID == "" {
			return RegistrySnapshot{}, registryProtocolError()
		}
	}
	return reg, nil
}
func (c *HomeAssistantClient) CreateArea(ctx context.Context, name string) (RegistryArea, error) {
	var area RegistryArea
	if strings.TrimSpace(name) == "" {
		return area, ErrRegistryUnavailable
	}
	s, close, e := c.registrySession(ctx)
	if e != nil {
		return area, e
	}
	defer close()
	e = s.request("config/area_registry/create", map[string]any{"name": name}, &area)
	if e == nil && area.ID == "" {
		e = registryProtocolError()
	}
	return area, e
}
func (c *HomeAssistantClient) SetDeviceArea(ctx context.Context, id string, areaID *string) error {
	s, close, e := c.registrySession(ctx)
	if e != nil {
		return e
	}
	defer close()
	var d RegistryDevice
	if e = s.request("config/device_registry/update", map[string]any{"device_id": id, "area_id": areaID}, &d); e != nil {
		return e
	}
	if d.ID != id {
		return registryProtocolError()
	}
	return nil
}
func (c *HomeAssistantClient) SetEntityArea(ctx context.Context, id string, areaID *string) error {
	s, close, e := c.registrySession(ctx)
	if e != nil {
		return e
	}
	defer close()
	var result struct {
		Entity RegistryEntity `json:"entity_entry"`
	}
	if e = s.request("config/entity_registry/update", map[string]any{"entity_id": id, "area_id": areaID}, &result); e != nil {
		return e
	}
	if result.Entity.EntityID != id {
		return registryProtocolError()
	}
	return nil
}
