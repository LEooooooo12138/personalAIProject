package smarthome

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type SourceStatus string

const (
	SourceAvailable    SourceStatus = "available"
	SourceUnsupported  SourceStatus = "unsupported"
	SourceForbidden    SourceStatus = "forbidden"
	SourceRetentionGap SourceStatus = "retention_gap"
	SourceUnavailable  SourceStatus = "unavailable"
)

type AutomationRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Exists nil means unknown; availability and resource existence are independent.
type AutomationConfigObservation struct {
	SourceStatus SourceStatus `json:"source_status"`
	Exists       *bool        `json:"exists,omitempty"`
	ConfigKey    string       `json:"config_key,omitempty"`
	Version      string       `json:"version,omitempty"`
	Digest       string       `json:"digest,omitempty"`
	Source       string       `json:"source"`
	ErrorCode    string       `json:"error_code,omitempty"`
	ObservedAt   time.Time    `json:"observed_at"`
	// Owned transient memory only, not a file/archive reference. Never log or
	// include in member DTOs. Future durable backups must fresh-read configuration.
	RawConfig json.RawMessage `json:"-"`
}

// Proof of observed loaded raw version only: these separate reads are not
// atomic and cannot prove trigger attachment, execution or physical success.
type RuntimeLoadProof struct {
	Verified       bool         `json:"verified"`
	ExpectedDigest string       `json:"expected_digest,omitempty"`
	ObservedDigest string       `json:"observed_digest,omitempty"`
	Source         string       `json:"source"`
	SourceStatus   SourceStatus `json:"source_status"`
	ErrorCode      string       `json:"error_code,omitempty"`
	ObservedAt     time.Time    `json:"observed_at"`
}
type AutomationObservation struct {
	Ref              AutomationRef               `json:"ref"`
	Ownership        string                      `json:"ownership"`
	EntityID         string                      `json:"entity_id,omitempty"`
	MappingSource    string                      `json:"mapping_source,omitempty"`
	MappingStatus    SourceStatus                `json:"mapping_status"`
	MappingErrorCode string                      `json:"mapping_error_code,omitempty"`
	State            string                      `json:"state,omitempty"`
	StateStatus      SourceStatus                `json:"state_status"`
	StateErrorCode   string                      `json:"state_error_code,omitempty"`
	StateObservedAt  time.Time                   `json:"state_observed_at"`
	Config           AutomationConfigObservation `json:"config"`
	RuntimeLoadProof RuntimeLoadProof            `json:"runtime_load_proof"`
}

func observationStatus(err error) SourceStatus {
	switch HAErrorCode(err) {
	case "ha_forbidden", "ha_auth_required":
		return SourceForbidden
	case "ha_unsupported":
		return SourceUnsupported
	default:
		return SourceUnavailable
	}
}
func validAutomationKey(key string) bool {
	if key == "" || len(key) > 255 || key == "." || key == ".." {
		return false
	}
	for _, r := range key {
		if r <= ' ' || strings.ContainsRune("/\\%?#", r) || r == 127 {
			return false
		}
	}
	return true
}
func validAutomationEntity(id string) bool {
	if !strings.HasPrefix(id, "automation.") || len(id) <= len("automation.") || len(id) > 255 {
		return false
	}
	for _, r := range strings.TrimPrefix(id, "automation.") {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// Refresh the exact reviewed profile every time. config.automation is not a
// required component marker: config registers its child routes directly.
func (c *HomeAssistantClient) automationProfile(ctx context.Context) (string, error) {
	config, err := c.GetConfig(ctx)
	if err != nil {
		return "", err
	}
	version, _ := config["version"].(string)
	if version != "2026.7.2" {
		return version, newHAError("ha_version_unknown")
	}
	components, ok := config["components"].([]any)
	if !ok {
		return version, newHAError("ha_capability_unknown")
	}
	var configLoaded, automationLoaded bool
	for _, v := range components {
		s, ok := v.(string)
		if !ok {
			return version, newHAError("ha_invalid_response")
		}
		configLoaded = configLoaded || s == "config"
		automationLoaded = automationLoaded || s == "automation"
	}
	if !configLoaded || !automationLoaded {
		return version, newHAError("ha_capability_unknown")
	}
	return version, nil
}
func (c *HomeAssistantClient) acquireObservation(ctx context.Context) (func(), error) {
	select {
	case c.observationReads <- struct{}{}:
		return func() { <-c.observationReads }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (c *HomeAssistantClient) ReadAutomationConfig(ctx context.Context, configKey string) (AutomationConfigObservation, error) {
	o := AutomationConfigObservation{SourceStatus: SourceUnavailable, Source: "config/automation/config", ConfigKey: configKey}
	if !validAutomationKey(configKey) {
		o.ErrorCode = "ha_invalid_target"
		return o, newHAError(o.ErrorCode)
	}
	release, err := c.acquireObservation(ctx)
	if err != nil {
		o.ErrorCode = HAErrorCode(err)
		return o, err
	}
	defer release()
	version, err := c.automationProfile(ctx)
	o.Version = version
	if err != nil {
		o.ErrorCode = HAErrorCode(err)
		o.SourceStatus = observationStatus(err)
		return o, err
	}
	return c.readAutomationConfig(ctx, configKey, version)
}
func (c *HomeAssistantClient) readAutomationConfig(ctx context.Context, key, version string) (AutomationConfigObservation, error) {
	o := AutomationConfigObservation{SourceStatus: SourceUnavailable, Source: "config/automation/config", ConfigKey: key, Version: version}
	raw, err := c.do(ctx, http.MethodGet, "/api/config/automation/config/"+url.PathEscape(key), nil)
	o.ObservedAt = time.Now().UTC()
	if err != nil {
		var status *haHTTPStatusError
		if errors.As(err, &status) && status.status == http.StatusNotFound {
			exists := false
			o.Exists = &exists
			o.SourceStatus = SourceAvailable
			return o, nil
		}
		o.SourceStatus = observationStatus(err)
		o.ErrorCode = HAErrorCode(err)
		return o, err
	}
	canonical, digest, err := canonicalAutomationConfig(raw)
	if err != nil {
		o.ErrorCode = HAErrorCode(err)
		return o, err
	}
	exists := true
	o.Exists = &exists
	o.SourceStatus = SourceAvailable
	o.RawConfig = canonical
	o.Digest = digest
	return o, nil
}

// Preserve number precision, array order, IDs, aliases, and unknown properties.
// Do not apply defaults or singular/plural key renames to manufacture equality.
func canonicalAutomationConfig(raw []byte) (json.RawMessage, string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeAutomationJSON(decoder, 0)
	if err != nil {
		return nil, "", newHAError("ha_invalid_response")
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, "", newHAError("ha_invalid_response")
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, "", newHAError("ha_invalid_response")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, "", newHAError("ha_invalid_response")
	}
	digest := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(digest[:]), nil
}

// Duplicate keys are ambiguous: reject instead of silently discarding fields.
func decodeAutomationJSON(decoder *json.Decoder, depth int) (any, error) {
	if depth > 100 {
		return nil, newHAError("ha_invalid_response")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		result := map[string]any{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, newHAError("ha_invalid_response")
			}
			if _, exists := result[name]; exists {
				return nil, newHAError("ha_invalid_response")
			}
			value, err := decodeAutomationJSON(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			result[name] = value
		}
		close, err := decoder.Token()
		if err != nil || close != json.Delim('}') {
			return nil, newHAError("ha_invalid_response")
		}
		return result, nil
	case '[':
		result := []any{}
		for decoder.More() {
			value, err := decodeAutomationJSON(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		close, err := decoder.Token()
		if err != nil || close != json.Delim(']') {
			return nil, newHAError("ha_invalid_response")
		}
		return result, nil
	default:
		return nil, newHAError("ha_invalid_response")
	}
}
func configHasID(raw json.RawMessage, key string) bool {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var id string
	return json.Unmarshal(value["id"], &id) == nil && id == key
}

// A7 must resolve managed IDs from durable records. Names, registry unique IDs,
// and legacy confirmed suggestions do not establish managed ownership.
func (m *Manager) ObserveAutomation(ctx context.Context, ref AutomationRef) (AutomationObservation, error) {
	empty := emptyAutomationObservation(ref)
	if ref.Kind == "managed" {
		record, err := m.GetManagedAutomation(ref.ID)
		if err != nil {
			return empty, newHAError("ha_managed_record_unavailable")
		}
		observed, err := m.observeAutomationConfigKey(ctx, record.ConfigKey, record.ExpectedDigest)
		observed.Ref = ref
		observed.Ownership = "managed"
		return observed, err
	}
	if (ref.Kind != "external" && ref.Kind != "unverified") || !validAutomationEntity(ref.ID) {
		return empty, newHAError("ha_invalid_target")
	}
	return m.client.observeAutomation(ctx, ref, ref.ID, "", "")
}

// Internal A7 seam: server-resolved config key + immutable expected digest.
// The caller must establish ownership independently; this never claims it.
func (m *Manager) observeAutomationConfigKey(ctx context.Context, key, expectedDigest string) (AutomationObservation, error) {
	ref := AutomationRef{Kind: "unverified", ID: key}
	if !validAutomationKey(key) || len(expectedDigest) != 64 {
		return emptyAutomationObservation(ref), newHAError("ha_invalid_target")
	}
	if _, err := hex.DecodeString(expectedDigest); err != nil {
		return emptyAutomationObservation(ref), newHAError("ha_invalid_target")
	}
	return m.client.observeAutomation(ctx, ref, "", key, expectedDigest)
}
func emptyAutomationObservation(ref AutomationRef) AutomationObservation {
	return AutomationObservation{Ref: ref, Ownership: "unverified", MappingStatus: SourceUnavailable, StateStatus: SourceUnavailable,
		Config:           AutomationConfigObservation{SourceStatus: SourceUnavailable, Source: "config/automation/config"},
		RuntimeLoadProof: RuntimeLoadProof{Source: "automation/config", SourceStatus: SourceUnavailable}}
}
func (c *HomeAssistantClient) observeAutomation(ctx context.Context, ref AutomationRef, entityID, key, expectedDigest string) (AutomationObservation, error) {
	o := emptyAutomationObservation(ref)
	release, err := c.acquireObservation(ctx)
	if err != nil {
		return o, err
	}
	defer release()
	version, err := c.automationProfile(ctx)
	o.Config.Version = version
	if err != nil {
		o.Config.ErrorCode = HAErrorCode(err)
		o.Config.SourceStatus = observationStatus(err)
		// An unreviewed profile cannot bypass server-observed selection. The
		// bounded registry read establishes only entity presence here, never a
		// version-specific config-key mapping or runtime-load proof.
		if entityID != "" {
			entities, selectionErr := c.readAutomationRegistry(ctx)
			if selectionErr != nil {
				o.StateErrorCode = HAErrorCode(selectionErr)
			} else {
				o.StateErrorCode = "ha_target_not_observed"
				for _, entity := range entities {
					if entity.EntityID == entityID {
						c.observeAutomationState(ctx, entityID, &o)
						break
					}
				}
			}
		}
		return o, err
	}
	knownKey := key != ""
	var configErr error
	if knownKey {
		o.Config, configErr = c.readAutomationConfig(ctx, key, version)
	}
	o.RuntimeLoadProof.ExpectedDigest = expectedDigest
	entities, err := c.readAutomationRegistry(ctx)
	if err != nil {
		o.MappingStatus = observationStatus(err)
		o.MappingErrorCode = HAErrorCode(err)
		return o, err
	}
	o.MappingStatus = SourceAvailable
	if entityID != "" {
		var selected *RegistryEntity
		for i := range entities {
			if entities[i].EntityID == entityID {
				if selected != nil {
					o.MappingErrorCode = "ha_mapping_ambiguous"
					return o, newHAError(o.MappingErrorCode)
				}
				selected = &entities[i]
			}
		}
		if selected == nil {
			o.MappingErrorCode = "ha_target_not_observed"
			return o, newHAError(o.MappingErrorCode)
		}
		o.Ownership = "external"
		c.observeAutomationState(ctx, entityID, &o)
		if selected.Platform == nil || *selected.Platform != "automation" || selected.UniqueID == nil || !validAutomationKey(*selected.UniqueID) {
			o.MappingErrorCode = "ha_mapping_unknown"
			return o, newHAError(o.MappingErrorCode)
		}
		key = *selected.UniqueID
	}
	var matching []RegistryEntity
	for _, entity := range entities {
		if entity.Platform != nil && *entity.Platform == "automation" && entity.UniqueID != nil && *entity.UniqueID == key {
			matching = append(matching, entity)
		}
	}
	if len(matching) != 1 {
		o.MappingErrorCode = "ha_mapping_ambiguous"
		if len(matching) == 0 {
			o.MappingErrorCode = "ha_mapping_unknown"
		}
		return o, newHAError(o.MappingErrorCode)
	}
	o.EntityID = matching[0].EntityID
	o.MappingSource = "config/entity_registry/list"
	if !knownKey {
		o.Config, configErr = c.readAutomationConfig(ctx, key, version)
	}
	if expectedDigest == "" {
		expectedDigest = o.Config.Digest
	}
	o.RuntimeLoadProof.ExpectedDigest = expectedDigest
	raw, runtimeErr := c.readAutomationRuntime(ctx, o.EntityID)
	o.RuntimeLoadProof.ObservedAt = time.Now().UTC()
	c.observeAutomationState(ctx, o.EntityID, &o)
	if runtimeErr != nil {
		o.RuntimeLoadProof.SourceStatus = observationStatus(runtimeErr)
		o.RuntimeLoadProof.ErrorCode = HAErrorCode(runtimeErr)
		return o, runtimeErr
	}
	canonical, digest, runtimeErr := canonicalAutomationConfig(raw)
	if runtimeErr != nil {
		o.RuntimeLoadProof.ErrorCode = HAErrorCode(runtimeErr)
		return o, runtimeErr
	}
	o.RuntimeLoadProof.SourceStatus = SourceAvailable
	o.RuntimeLoadProof.ObservedDigest = digest
	o.RuntimeLoadProof.Verified = expectedDigest != "" && digest == expectedDigest && configHasID(canonical, key) && (o.State == "on" || o.State == "off") && o.StateStatus == SourceAvailable
	return o, configErr
}
func (c *HomeAssistantClient) observeAutomationState(ctx context.Context, id string, o *AutomationObservation) {
	state, err := c.GetState(ctx, id)
	o.StateObservedAt = time.Now().UTC()
	o.State = ""
	if err != nil {
		o.StateStatus = observationStatus(err)
		o.StateErrorCode = HAErrorCode(err)
		return
	}
	o.EntityID = id
	o.State = state.State
	o.StateStatus = SourceAvailable
	o.StateErrorCode = ""
}
func (c *HomeAssistantClient) readAutomationRegistry(ctx context.Context) ([]RegistryEntity, error) {
	session, close, err := c.registrySession(ctx)
	if err != nil {
		return nil, err
	}
	defer close()
	var entities []RegistryEntity
	if err = session.request("config/entity_registry/list", nil, &entities); err != nil {
		return nil, err
	}
	if entities == nil || len(entities) > 10000 {
		return nil, newHAError("ha_invalid_response")
	}
	result := []RegistryEntity{}
	seen := map[string]bool{}
	for _, entity := range entities {
		if entity.EntityID == "" || seen[entity.EntityID] {
			return nil, newHAError("ha_invalid_response")
		}
		seen[entity.EntityID] = true
		if strings.HasPrefix(entity.EntityID, "automation.") {
			if !validAutomationEntity(entity.EntityID) {
				return nil, newHAError("ha_invalid_response")
			}
			result = append(result, entity)
		}
	}
	return result, nil
}
func (c *HomeAssistantClient) readAutomationRuntime(ctx context.Context, id string) (json.RawMessage, error) {
	session, close, err := c.registrySession(ctx)
	if err != nil {
		return nil, err
	}
	defer close()
	var result struct {
		Config json.RawMessage `json:"config"`
	}
	if err = session.request("automation/config", map[string]any{"entity_id": id}, &result); err != nil {
		return nil, err
	}
	return result.Config, nil
}
