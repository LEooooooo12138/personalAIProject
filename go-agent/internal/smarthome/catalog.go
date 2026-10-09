package smarthome

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type CatalogSource interface {
	GetRegistry(context.Context) (RegistrySnapshot, error)
	GetStates(context.Context) ([]EntityState, error)
}

// CatalogSnapshot keeps registry internals private; projection methods return independent DTOs.
type CatalogSnapshot struct {
	Meta    CatalogMeta
	areas   map[string]AreaRef
	devices map[string]map[string]DeviceView
	totals  CatalogTotals
}
type CatalogService struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	source   CatalogSource
	snapshot CatalogSnapshot
	refresh  chan struct{}
	workers  sync.WaitGroup
	closed   bool
}

func NewCatalogService(parent context.Context, source CatalogSource) *CatalogService {
	ctx, cancel := context.WithCancel(parent)
	return &CatalogService{ctx: ctx, cancel: cancel, source: source, snapshot: CatalogSnapshot{Meta: CatalogMeta{Freshness: "unknown", Connection: "unknown"}}}
}
func (c *CatalogService) Close() {
	c.mu.Lock()
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	c.workers.Wait()
}
func (c *CatalogService) Get(ctx context.Context) (CatalogSnapshot, error) {
	if e := ctx.Err(); e != nil {
		return CatalogSnapshot{}, e
	}
	c.mu.Lock()
	if c.closed || c.ctx.Err() != nil {
		c.mu.Unlock()
		return CatalogSnapshot{}, ErrCatalogUnavailable
	}
	if c.snapshot.areas != nil && c.snapshot.Meta.Connection == "connected" && c.snapshot.Meta.ObservedAt != nil && time.Since(*c.snapshot.Meta.ObservedAt) <= 30*time.Second {
		out := c.snapshot
		out.Meta = cloneMeta(out.Meta)
		c.mu.Unlock()
		return out, nil
	}
	done := c.refresh
	if done == nil {
		done = make(chan struct{})
		c.refresh = done
		c.workers.Add(1)
		go c.fetch(done)
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return CatalogSnapshot{}, ctx.Err()
	case <-done:
	}
	c.mu.Lock()
	out := c.snapshot
	out.Meta = cloneMeta(out.Meta)
	c.mu.Unlock()
	if out.areas == nil {
		if out.Meta.ErrorCode != nil {
			return out, fmt.Errorf("%w: %w", ErrCatalogUnavailable, newHAError(*out.Meta.ErrorCode))
		}
		return out, ErrCatalogUnavailable
	}
	return out, nil
}
func (c *CatalogService) fetch(done chan struct{}) {
	defer c.workers.Done()
	attempt := time.Now().UTC()
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	reg, e := c.source.GetRegistry(ctx)
	var states []EntityState
	var next CatalogSnapshot
	if e == nil {
		states, e = c.source.GetStates(ctx)
	}
	if e == nil {
		next, e = buildCatalog(reg, states)
		if e != nil {
			e = newHAError("ha_invalid_response")
		}
	}
	if e == nil {
		e = ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e == nil {
		observed := time.Now().UTC()
		next.Meta = CatalogMeta{ObservedAt: &observed, LastAttemptAt: &attempt, LastSuccessAt: &observed, Freshness: "fresh", Connection: "connected"}
		c.snapshot = next
	} else {
		code := HAErrorCode(e)
		c.snapshot.Meta.LastAttemptAt = &attempt
		c.snapshot.Meta.Connection = "unavailable"
		c.snapshot.Meta.ErrorCode = &code
		c.snapshot.Meta.Freshness = "unknown"
		if c.snapshot.areas != nil {
			c.snapshot.Meta.Freshness = "stale"
		}
	}
	c.refresh = nil
	close(done)
}
func catalogID(prefix, raw string) string {
	return prefix + base64.RawURLEncoding.EncodeToString([]byte(raw))
}
func validCatalogID(id, prefix string) bool {
	if !strings.HasPrefix(id, prefix) {
		return false
	}
	v := strings.TrimPrefix(id, prefix)
	raw, e := base64.RawURLEncoding.DecodeString(v)
	return e == nil && len(raw) > 0 && utf8.Valid(raw) && base64.RawURLEncoding.EncodeToString(raw) == v
}
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
func cloneMeta(m CatalogMeta) CatalogMeta {
	m.ObservedAt = clonePtr(m.ObservedAt)
	m.LastSuccessAt = clonePtr(m.LastSuccessAt)
	m.LastAttemptAt = clonePtr(m.LastAttemptAt)
	m.ErrorCode = clonePtr(m.ErrorCode)
	return m
}
func cloneDevice(d DeviceView) DeviceView {
	d.DirectAreaID = clonePtr(d.DirectAreaID)
	d.Domains = append([]string{}, d.Domains...)
	d.Entities = append([]EntityView{}, d.Entities...)
	for i := range d.Entities {
		e := &d.Entities[i]
		e.State = clonePtr(e.State)
		e.Unit = clonePtr(e.Unit)
		e.LastChanged = clonePtr(e.LastChanged)
		e.LastUpdated = clonePtr(e.LastUpdated)
		e.Category = clonePtr(e.Category)
		e.DeviceClass = clonePtr(e.DeviceClass)
	}
	return d
}
func deviceName(d RegistryDevice) string {
	if d.NameByUser != nil && *d.NameByUser != "" {
		return *d.NameByUser
	}
	if d.Name != nil && *d.Name != "" {
		return *d.Name
	}
	return d.ID
}
func entityName(e RegistryEntity) string {
	if e.Name != nil && *e.Name != "" {
		return *e.Name
	}
	if e.OriginalName != nil && *e.OriginalName != "" {
		return *e.OriginalName
	}
	return e.EntityID
}
func buildCatalog(reg RegistrySnapshot, states []EntityState) (CatalogSnapshot, error) {
	s := CatalogSnapshot{areas: map[string]AreaRef{}, devices: map[string]map[string]DeviceView{}}
	rawAreas := map[string]string{}
	other := "u_other"
	for _, a := range reg.Areas {
		if a.ID == "" || !utf8.ValidString(a.ID) || rawAreas[a.ID] != "" {
			return s, ErrCatalogUnavailable
		}
		id := catalogID("a_", a.ID)
		rawAreas[a.ID] = id
		s.areas[id] = AreaRef{ID: id, Name: a.Name}
		if a.Name == "其他" && other == "u_other" {
			other = id
		}
	}
	if other == "u_other" {
		s.areas[other] = AreaRef{ID: other, Name: "其他"}
	}
	for id := range s.areas {
		s.devices[id] = map[string]DeviceView{}
	}
	area := func(raw *string) string {
		if raw != nil {
			if id := rawAreas[*raw]; id != "" {
				return id
			}
		}
		return other
	}
	devs := map[string]RegistryDevice{}
	direct := map[string]string{}
	for _, d := range reg.Devices {
		if d.ID == "" || !utf8.ValidString(d.ID) {
			return s, ErrCatalogUnavailable
		}
		if _, ok := devs[d.ID]; ok {
			return s, ErrCatalogUnavailable
		}
		devs[d.ID] = d
		direct[d.ID] = area(d.AreaID)
		s.totals.Devices++
		id := catalogID("d_", d.ID)
		var rawDirect *string
		if d.AreaID != nil && *d.AreaID != "" {
			v := catalogID("a_", *d.AreaID)
			rawDirect = &v
		}
		s.devices[direct[d.ID]][id] = DeviceView{ID: id, Kind: "device", Name: deviceName(d), AreaID: direct[d.ID], DirectAreaID: rawDirect, Membership: "direct", Domains: []string{}, Entities: []EntityView{}}
	}
	stateByID := map[string]EntityState{}
	for _, st := range states {
		stateByID[st.EntityID] = st
	}
	seen := map[string]bool{}
	for _, e := range reg.Entities {
		if e.EntityID == "" || !utf8.ValidString(e.EntityID) || seen[e.EntityID] {
			return s, ErrCatalogUnavailable
		}
		seen[e.EntityID] = true
		s.totals.Entities++
		aid := other
		var d RegistryDevice
		hasDevice := false
		if e.DeviceID != nil {
			d, hasDevice = devs[*e.DeviceID]
		}
		if e.AreaID != nil && *e.AreaID != "" {
			aid = area(e.AreaID)
		} else if hasDevice {
			aid = direct[d.ID]
		}
		view := EntityView{EntityID: e.EntityID, Name: entityName(e), Domain: strings.SplitN(e.EntityID, ".", 2)[0], Disabled: e.DisabledBy != nil, Hidden: e.HiddenBy != nil, Category: clonePtr(e.Category)}
		if st, ok := stateByID[e.EntityID]; ok {
			view.State = clonePtr(&st.State)
			view.DeviceClass = safeDeviceClass(st.Attributes["device_class"])
			if u, ok := st.Attributes["unit_of_measurement"].(string); ok {
				view.Unit = &u
			}
			if !st.LastChanged.IsZero() {
				view.LastChanged = clonePtr(&st.LastChanged)
			}
			if !st.LastUpdated.IsZero() {
				view.LastUpdated = clonePtr(&st.LastUpdated)
			}
		}
		id := catalogID("e_", e.EntityID)
		dv := DeviceView{ID: id, Kind: "entity", Name: view.Name, AreaID: aid, Membership: "entity", Domains: []string{}, Entities: []EntityView{}}
		if hasDevice {
			id = catalogID("d_", d.ID)
			dv, _ = s.devices[aid][id]
			if dv.ID == "" {
				var rawDirect *string
				if d.AreaID != nil && *d.AreaID != "" {
					v := catalogID("a_", *d.AreaID)
					rawDirect = &v
				}
				dv = DeviceView{ID: id, Kind: "device", Name: deviceName(d), AreaID: aid, DirectAreaID: rawDirect, Membership: "entity", Domains: []string{}, Entities: []EntityView{}}
			} else if direct[d.ID] == aid {
				dv.Membership = "both"
			}
		} else {
			s.totals.StandaloneEntities++
		}
		dv.Entities = append(dv.Entities, view)
		dv.EntityCount = len(dv.Entities)
		s.devices[aid][id] = dv
	}
	for aid, items := range s.devices {
		for id, d := range items {
			domains := map[string]bool{}
			for _, e := range d.Entities {
				domains[e.Domain] = true
			}
			for domain := range domains {
				d.Domains = append(d.Domains, domain)
			}
			sort.Strings(d.Domains)
			sort.Slice(d.Entities, func(i, j int) bool { return d.Entities[i].EntityID < d.Entities[j].EntityID })
			s.devices[aid][id] = d
		}
	}
	return s, nil
}
func (s CatalogSnapshot) ListAreas(query string) (AreaList, error) {
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) > 128 {
		return AreaList{}, ErrCatalogInvalid
	}
	if s.areas == nil {
		return AreaList{}, ErrCatalogUnavailable
	}
	q := strings.ToLower(strings.TrimSpace(query))
	list := AreaList{Meta: cloneMeta(s.Meta), Totals: s.totals, Areas: []AreaView{}}
	for id, ref := range s.areas {
		a := AreaView{ID: id, Name: ref.Name, Matches: []AreaMatch{}}
		areaMatch := q == "" || strings.Contains(strings.ToLower(ref.Name), q)
		for _, d := range s.devices[id] {
			if d.Kind == "device" {
				a.DeviceCount++
			} else {
				a.StandaloneEntityCount++
			}
			a.EntityCount += d.EntityCount
			match := areaMatch || strings.Contains(strings.ToLower(d.Name), q)
			for _, e := range d.Entities {
				if strings.Contains(strings.ToLower(e.Name), q) || strings.Contains(strings.ToLower(e.EntityID), q) {
					match = true
				}
			}
			if match {
				a.Matches = append(a.Matches, AreaMatch{ID: d.ID, Name: d.Name, Kind: d.Kind})
			}
		}
		sort.Slice(a.Matches, func(i, j int) bool { return a.Matches[i].ID < a.Matches[j].ID })
		if areaMatch || len(a.Matches) > 0 {
			list.Areas = append(list.Areas, a)
		}
	}
	sort.Slice(list.Areas, func(i, j int) bool { return list.Areas[i].ID < list.Areas[j].ID })
	return list, nil
}
func (s CatalogSnapshot) ListDevices(areaID string) (AreaDevices, error) {
	if s.areas == nil {
		return AreaDevices{}, ErrCatalogUnavailable
	}
	if areaID != "u_other" && !validCatalogID(areaID, "a_") {
		return AreaDevices{}, ErrCatalogNotFound
	}
	ref, ok := s.areas[areaID]
	if !ok {
		return AreaDevices{}, ErrCatalogNotFound
	}
	out := AreaDevices{Meta: cloneMeta(s.Meta), Area: ref, Devices: []DeviceView{}}
	for _, d := range s.devices[areaID] {
		out.Devices = append(out.Devices, cloneDevice(d))
	}
	sort.Slice(out.Devices, func(i, j int) bool { return out.Devices[i].ID < out.Devices[j].ID })
	return out, nil
}
func (s CatalogSnapshot) Device(areaID, itemID string) (AreaDeviceDetail, error) {
	a, e := s.ListDevices(areaID)
	if e != nil {
		return AreaDeviceDetail{}, e
	}
	if !validCatalogID(itemID, "d_") && !validCatalogID(itemID, "e_") {
		return AreaDeviceDetail{}, ErrCatalogNotFound
	}
	for _, d := range a.Devices {
		if d.ID == itemID {
			return AreaDeviceDetail{Meta: a.Meta, Area: a.Area, Device: d}, nil
		}
	}
	return AreaDeviceDetail{}, ErrCatalogNotFound
}

func safeDeviceClass(raw any) *string {
	value, ok := raw.(string)
	if !ok || len(value) > 64 {
		return nil
	}
	switch value {
	case "door", "window", "opening", "tamper", "motion", "occupancy", "presence", "smoke", "gas", "moisture", "safety", "problem", "connectivity", "battery", "battery_charging", "plug", "power", "running", "sound", "vibration", "lock", "heat", "cold", "light", "temperature", "humidity", "illuminance", "energy", "voltage", "current", "carbon_dioxide", "carbon_monoxide", "pressure", "distance", "duration", "timestamp":
		return &value
	default:
		return nil
	}
}
