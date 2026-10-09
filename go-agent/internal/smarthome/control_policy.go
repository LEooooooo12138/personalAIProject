package smarthome

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var controlEntityPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z0-9_]+$`)

func normalizeControlConfig(c ControlConfig) (ControlConfig, string, error) {
	d := DefaultControlConfig()
	if c.ProposalTTL == 0 {
		c.ProposalTTL = d.ProposalTTL
	}
	if c.ReadbackTimeout == 0 {
		c.ReadbackTimeout = d.ReadbackTimeout
	}
	if c.ReadbackInterval == 0 {
		c.ReadbackInterval = d.ReadbackInterval
	}
	if c.Retention == 0 {
		c.Retention = d.Retention
	}
	if c.MaxRecords == 0 {
		c.MaxRecords = d.MaxRecords
	}
	if c.ProposalTTL < time.Second || c.ProposalTTL > 24*time.Hour || c.ReadbackTimeout < time.Millisecond || c.ReadbackTimeout > time.Minute || c.ReadbackInterval < time.Millisecond || c.ReadbackInterval > c.ReadbackTimeout || c.Retention < time.Hour || c.Retention > 365*24*time.Hour || c.MaxRecords < 1 || c.MaxRecords > 10000 {
		return c, "", ErrControlInvalid
	}
	targets := make([]ControlTarget, len(c.Targets))
	seen := map[string]bool{}
	for i, v := range c.Targets {
		domain := strings.SplitN(v.EntityID, ".", 2)[0]
		if !controlEntityPattern.MatchString(v.EntityID) || seen[v.EntityID] || (domain != "light" && domain != "switch" && domain != "fan") || !v.LoadLocationVerified || !validControlName(v.Name) || !validControlName(v.AreaName) || len(v.AllowedActions) == 0 || len(v.AllowedActions) > 2 || (v.Domain != "" && v.Domain != domain) {
			return c, "", ErrControlInvalid
		}
		seen[v.EntityID] = true
		v.Domain = domain
		actions := map[string]bool{}
		for _, a := range v.AllowedActions {
			if (a != "turn_on" && a != "turn_off") || actions[a] {
				return c, "", ErrControlInvalid
			}
			actions[a] = true
		}
		if len(v.Aliases) > 8 {
			return c, "", ErrControlInvalid
		}
		aliases := map[string]bool{}
		for _, a := range v.Aliases {
			if !validControlName(a) || aliases[a] {
				return c, "", ErrControlInvalid
			}
			aliases[a] = true
		}
		v.AllowedActions = append([]string{}, v.AllowedActions...)
		sort.Strings(v.AllowedActions)
		v.Aliases = append([]string{}, v.Aliases...)
		sort.Strings(v.Aliases)
		targets[i] = v
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].EntityID < targets[j].EntityID })
	c.Targets = targets
	raw, _ := json.Marshal(targets)
	hash := sha256.Sum256(raw)
	return c, hex.EncodeToString(hash[:]), nil
}
func validControlName(v string) bool {
	return strings.TrimSpace(v) != "" && utf8.ValidString(v) && utf8.RuneCountInString(v) <= 128 && len(v) <= 256 && !strings.ContainsAny(v, "\x00\r\n")
}
func controlMatch(q string, names ...string) bool {
	if q == "" {
		return true
	}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" && (q == n || strings.Contains(q, n)) {
			return true
		}
	}
	return false
}

// QueryTargets projects only visible, enabled registry entries, with no raw attributes.
func (s CatalogSnapshot) QueryTargets() []QueryTarget {
	out := []QueryTarget{}
	seen := map[string]bool{}
	for aid, devices := range s.devices {
		for _, d := range devices {
			for _, e := range d.Entities {
				if e.Hidden || e.Disabled || seen[e.EntityID] || !controlEntityPattern.MatchString(e.EntityID) {
					continue
				}
				seen[e.EntityID] = true
				out = append(out, QueryTarget{EntityID: e.EntityID, Name: e.Name, AreaName: s.areas[aid].Name, Domain: e.Domain})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EntityID < out[j].EntityID })
	return out
}
func (s *ControlService) Candidates(ctx context.Context, query string) ([]QueryTarget, []ControlTarget, error) {
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) > 8192 {
		return nil, nil, ErrControlInvalid
	}
	snap, e := s.catalog.Get(ctx)
	if e != nil {
		return nil, nil, ErrControlUnavailable
	}
	q := strings.ToLower(strings.TrimSpace(query))
	queries := []QueryTarget{}
	controls := []ControlTarget{}
	for _, v := range snap.QueryTargets() {
		target, ok := s.targets[v.EntityID]
		names := []string{v.EntityID, v.Name, v.AreaName, v.AreaName + v.Name}
		if ok {
			names = append(names, target.Name, target.AreaName, target.AreaName+target.Name)
			names = append(names, target.Aliases...)
		}
		if !controlMatch(q, names...) {
			continue
		}
		queries = append(queries, v)
		if ok && snap.Meta.Freshness == "fresh" && snap.Meta.Connection == "connected" {
			controls = append(controls, cloneControlTarget(target))
		}
	}
	if len(queries) > 20 || len(controls) > 20 {
		return nil, nil, ErrControlUnsupported
	}
	return queries, controls, nil
}
func (s *ControlService) ResolveQueryTargets(ctx context.Context, q string) ([]QueryTarget, error) {
	v, _, e := s.Candidates(ctx, q)
	return v, e
}
func (s *ControlService) ResolveControlTargets(ctx context.Context, q string) ([]ControlTarget, error) {
	_, v, e := s.Candidates(ctx, q)
	return v, e
}
func (s *ControlService) ValidateControlTarget(ctx context.Context, actor ControlActor, id, action string) (ControlTarget, error) {
	if actor.UserID == "" || actor.SessionID == "" {
		return ControlTarget{}, ErrControlForbidden
	}
	v, ok := s.targets[id]
	if !ok {
		return v, ErrControlUnsupported
	}
	allowed := false
	for _, a := range v.AllowedActions {
		allowed = allowed || a == action
	}
	if !allowed {
		return v, ErrControlUnsupported
	}
	snap, e := s.catalog.Get(ctx)
	if e != nil || snap.Meta.Freshness != "fresh" || snap.Meta.Connection != "connected" {
		return v, ErrControlUnavailable
	}
	for _, q := range snap.QueryTargets() {
		if q.EntityID == id {
			return cloneControlTarget(v), nil
		}
	}
	return v, ErrControlConflict
}
func cloneControlTarget(v ControlTarget) ControlTarget {
	v.Aliases = append([]string{}, v.Aliases...)
	v.AllowedActions = append([]string{}, v.AllowedActions...)
	return v
}
