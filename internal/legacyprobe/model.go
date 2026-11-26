package legacyprobe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

type ProfileMode string

const (
	ModeHosts ProfileMode = "hosts"
	ModeDNS   ProfileMode = "dns"
)

type OriginalDNSKind string

const (
	OriginalDNSManual    OriginalDNSKind = "manual"
	OriginalDNSDHCPEmpty OriginalDNSKind = "dhcp_empty"
)

type OriginalDNS struct {
	Kind    OriginalDNSKind `json:"kind"`
	Servers []string        `json:"servers,omitempty"`
}

func (d *OriginalDNS) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return fmt.Errorf("empty original_dns")
	}
	switch data[0] {
	case '[':
		var servers []string
		if err := json.Unmarshal(data, &servers); err != nil {
			return fmt.Errorf("decode legacy original_dns: %w", err)
		}
		servers = filterLocalResolvers(servers)
		if len(servers) == 0 || slicesContain(servers, "Empty") {
			d.Kind = OriginalDNSDHCPEmpty
			d.Servers = nil
			return nil
		}
		d.Kind = OriginalDNSManual
		d.Servers = servers
		return nil
	case '{':
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("decode tagged original_dns: %w", err)
		}
		kindRaw, ok := raw["kind"]
		if !ok {
			return fmt.Errorf("original_dns.kind is required")
		}
		var kind OriginalDNSKind
		if err := json.Unmarshal(kindRaw, &kind); err != nil {
			return fmt.Errorf("decode original_dns.kind: %w", err)
		}
		switch kind {
		case OriginalDNSDHCPEmpty:
			d.Kind = kind
			d.Servers = nil
			return nil
		case OriginalDNSManual:
			serversRaw, ok := raw["servers"]
			if !ok || bytes.Equal(bytes.TrimSpace(serversRaw), []byte("null")) {
				return fmt.Errorf("original_dns.servers is required for manual DNS")
			}
			var servers []string
			if err := json.Unmarshal(serversRaw, &servers); err != nil {
				return fmt.Errorf("decode original_dns.servers: %w", err)
			}
			servers = filterLocalResolvers(servers)
			if len(servers) == 0 {
				d.Kind = OriginalDNSDHCPEmpty
				d.Servers = nil
				return nil
			}
			d.Kind = kind
			d.Servers = servers
			return nil
		default:
			return fmt.Errorf("unknown original_dns kind %q", kind)
		}
	default:
		return fmt.Errorf("original_dns must be a tagged object or legacy array")
	}
}

type Manifest struct {
	Version     int          `json:"version"`
	AppVersion  string       `json:"app_version"`
	UpdatedAt   time.Time    `json:"updated_at"`
	DNSEnabled  *bool        `json:"dns_enabled"`
	OriginalDNS *OriginalDNS `json:"original_dns"`
}

func (m *Manifest) UnmarshalJSON(data []byte) error {
	type manifestAlias Manifest
	var decoded manifestAlias
	if err := decodeObject(data, &decoded, "version", "app_version", "updated_at"); err != nil {
		return err
	}
	*m = Manifest(decoded)
	return nil
}

type RuleSource struct {
	Type       string `json:"type"`
	SourceID   string `json:"source_id,omitempty"`
	SourceName string `json:"source_name,omitempty"`
}

type HostRule struct {
	ID      string      `json:"id"`
	IP      *netip.Addr `json:"ip"`
	Domains []string    `json:"domains"`
	Enabled bool        `json:"enabled"`
	Comment *string     `json:"comment"`
	Source  RuleSource  `json:"source"`
}

func (r *HostRule) UnmarshalJSON(data []byte) error {
	type ruleAlias HostRule
	var decoded ruleAlias
	if err := decodeObject(data, &decoded, "id", "domains", "enabled", "source"); err != nil {
		return err
	}
	if !validUUID(decoded.ID) {
		return fmt.Errorf("invalid rule id %q", decoded.ID)
	}
	switch decoded.Source.Type {
	case "Manual":
	case "Remote", "AdBlock":
		if decoded.Source.SourceID == "" || decoded.Source.SourceName == "" {
			return fmt.Errorf("remote rule source requires source_id and source_name")
		}
	default:
		return fmt.Errorf("unknown rule source type %q", decoded.Source.Type)
	}
	*r = HostRule(decoded)
	return nil
}

type Profile struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description *string     `json:"description"`
	Enabled     bool        `json:"enabled"`
	Protected   bool        `json:"protected"`
	Tags        []string    `json:"tags"`
	Rules       []HostRule  `json:"rules"`
	Mode        ProfileMode `json:"mode"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

func (p *Profile) UnmarshalJSON(data []byte) error {
	type profileAlias Profile
	decoded := profileAlias{Mode: ModeHosts}
	if err := decodeObject(data, &decoded, "id", "name", "enabled", "protected", "tags", "rules", "created_at", "updated_at"); err != nil {
		return err
	}
	if !validUUID(decoded.ID) {
		return fmt.Errorf("invalid profile id %q", decoded.ID)
	}
	if decoded.Mode != ModeHosts && decoded.Mode != ModeDNS {
		return fmt.Errorf("unknown profile mode %q", decoded.Mode)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if raw, ok := fields["mode"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("profile mode cannot be null")
	}
	*p = Profile(decoded)
	return nil
}

type SnapshotMeta struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  *string   `json:"description"`
	ProfileCount int       `json:"profile_count"`
	CreatedAt    time.Time `json:"created_at"`
}

type snapshotIndex struct {
	Version   int            `json:"version"`
	Snapshots []SnapshotMeta `json:"snapshots"`
}

type Snapshot struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Profiles    []Profile `json:"profiles"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Snapshot) UnmarshalJSON(data []byte) error {
	type snapshotAlias Snapshot
	var decoded snapshotAlias
	if err := decodeObject(data, &decoded, "id", "name", "profiles", "created_at"); err != nil {
		return err
	}
	*s = Snapshot(decoded)
	return nil
}

type adBlockSource struct {
	SourceID            string     `json:"source_id"`
	Name                string     `json:"name"`
	URL                 string     `json:"url"`
	Enabled             bool       `json:"enabled"`
	Response            string     `json:"response"`
	Format              string     `json:"format"`
	LastFetchedAt       *time.Time `json:"last_fetched_at"`
	LastError           *string    `json:"last_error"`
	RuleCount           int        `json:"rule_count"`
	ETag                *string    `json:"etag"`
	RulesLimitOverride  *int       `json:"rules_limit_override"`
	LastRefreshDuration *uint64    `json:"last_refresh_duration_ms"`
	LastRefreshFailedAt *time.Time `json:"last_refresh_failed_at"`
}

func (s *adBlockSource) UnmarshalJSON(data []byte) error {
	type sourceAlias adBlockSource
	decoded := sourceAlias{Format: "hosts"}
	if err := decodeObject(data, &decoded, "source_id", "name", "url", "enabled", "response", "rule_count"); err != nil {
		return err
	}
	if decoded.Format != "hosts" && decoded.Format != "domains" {
		return fmt.Errorf("unknown blocklist format %q", decoded.Format)
	}
	*s = adBlockSource(decoded)
	return nil
}

type adBlockState struct {
	Enabled              bool            `json:"enabled"`
	Sources              []adBlockSource `json:"sources"`
	Whitelist            []string        `json:"whitelist"`
	AutoRefreshEnabled   bool            `json:"auto_refresh_enabled"`
	RefreshIntervalHours uint32          `json:"refresh_interval_hours"`
}

func (s *adBlockState) UnmarshalJSON(data []byte) error {
	type stateAlias adBlockState
	var decoded stateAlias
	if err := decodeObject(data, &decoded, "enabled", "sources", "whitelist", "auto_refresh_enabled", "refresh_interval_hours"); err != nil {
		return err
	}
	*s = adBlockState(decoded)
	return nil
}

func decodeObject(data []byte, target any, required ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("decode JSON object: %w", err)
	}
	for _, name := range required {
		raw, ok := fields[name]
		if !ok {
			return fmt.Errorf("required field %q is missing", name)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("required field %q cannot be null", name)
		}
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode JSON fields: %w", err)
	}
	return nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, r := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

func filterLocalResolvers(servers []string) []string {
	filtered := make([]string, 0, len(servers))
	for _, server := range servers {
		if !isLocalResolver(server) {
			filtered = append(filtered, server)
		}
	}
	return filtered
}

func isLocalResolver(server string) bool {
	if address, err := netip.ParseAddr(server); err == nil {
		return address.IsLoopback() || address.IsUnspecified()
	}
	if address, err := netip.ParseAddrPort(server); err == nil {
		return address.Addr().IsLoopback() || address.Addr().IsUnspecified()
	}
	return false
}

func slicesContain(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
