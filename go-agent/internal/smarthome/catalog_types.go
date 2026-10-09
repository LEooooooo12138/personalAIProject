package smarthome

import (
	"errors"
	"time"
)

var ErrCatalogNotFound = errors.New("catalog item not found")
var ErrCatalogInvalid = errors.New("invalid catalog query")
var ErrCatalogUnavailable = errors.New("catalog unavailable")

type CatalogMeta struct {
	ObservedAt    *time.Time `json:"observed_at"`
	LastAttemptAt *time.Time `json:"last_attempt_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	Freshness     string     `json:"freshness"`
	Connection    string     `json:"connection"`
	ErrorCode     *string    `json:"error_code"`
}
type AreaRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type CatalogTotals struct {
	Devices            int `json:"devices"`
	Entities           int `json:"entities"`
	StandaloneEntities int `json:"standalone_entities"`
}
type AreaMatch struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}
type AreaView struct {
	ID                    string      `json:"id"`
	Name                  string      `json:"name"`
	DeviceCount           int         `json:"device_count"`
	EntityCount           int         `json:"entity_count"`
	StandaloneEntityCount int         `json:"standalone_entity_count"`
	Matches               []AreaMatch `json:"matches"`
}
type AreaList struct {
	Meta   CatalogMeta   `json:"meta"`
	Totals CatalogTotals `json:"totals"`
	Areas  []AreaView    `json:"areas"`
}
type AreaDevices struct {
	Meta    CatalogMeta  `json:"meta"`
	Area    AreaRef      `json:"area"`
	Devices []DeviceView `json:"devices"`
}
type AreaDeviceDetail struct {
	Meta   CatalogMeta `json:"meta"`
	Area   AreaRef     `json:"area"`
	Device DeviceView  `json:"device"`
}
type DeviceView struct {
	ID                   string       `json:"id"`
	Kind                 string       `json:"kind"`
	Name                 string       `json:"name"`
	AreaID               string       `json:"area_id"`
	DirectAreaID         *string      `json:"direct_area_id"`
	Membership           string       `json:"membership"`
	EntityCount          int          `json:"entity_count"`
	Domains              []string     `json:"domains"`
	Entities             []EntityView `json:"entities"`
	LoadLocationVerified bool         `json:"load_location_verified"`
}
type EntityView struct {
	DeviceClass *string    `json:"device_class"`
	EntityID    string     `json:"entity_id"`
	Name        string     `json:"name"`
	Domain      string     `json:"domain"`
	State       *string    `json:"state"`
	Unit        *string    `json:"unit"`
	LastChanged *time.Time `json:"last_changed"`
	LastUpdated *time.Time `json:"last_updated"`
	Disabled    bool       `json:"disabled"`
	Hidden      bool       `json:"hidden"`
	Category    *string    `json:"category"`
}
