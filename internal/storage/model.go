package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// ModelErrKind 区分模型解码失败的类别：语法错误（JSON 损坏）与
// 校验错误（缺字段、错类型、UUID/领域校验失败）。服务层据此映射到
// invalid_json 与 validation_failed。
type ModelErrKind string

const (
	ModelErrSyntax     ModelErrKind = "syntax"
	ModelErrValidation ModelErrKind = "validation"
)

// ModelError 是存储模型解码/校验失败的统一错误类型。
type ModelError struct {
	Kind    ModelErrKind
	Message string
}

func (e *ModelError) Error() string { return e.Message }

func syntaxErrorf(format string, args ...any) *ModelError {
	return &ModelError{Kind: ModelErrSyntax, Message: fmt.Sprintf(format, args...)}
}

func validationErrorf(format string, args ...any) *ModelError {
	return &ModelError{Kind: ModelErrValidation, Message: fmt.Sprintf(format, args...)}
}

// decodeModel 先按 syntax 错误解析 JSON，再对 required 字段做
// null/缺失校验，最后交给 target 的领域 UnmarshalJSON。
func decodeModel(data []byte, target any, required ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return syntaxErrorf("decode JSON object: %v", err)
	}
	for _, name := range required {
		raw, ok := fields[name]
		if !ok {
			return validationErrorf("required field %q is missing", name)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return validationErrorf("required field %q cannot be null", name)
		}
	}
	if err := json.Unmarshal(data, target); err != nil {
		var modelErr *ModelError
		if errors.As(err, &modelErr) {
			return err
		}
		return validationErrorf("decode JSON fields: %v", err)
	}
	return nil
}

// ProfileMode 是 Profile 的模式：hosts 改写或 DNS 映射。
type ProfileMode string

const (
	ModeHosts ProfileMode = "hosts"
	ModeDNS   ProfileMode = "dns"
)

// OriginalDNSKind 标记迁移时记录的原始 DNS 状态。
type OriginalDNSKind string

const (
	OriginalDNSManual    OriginalDNSKind = "manual"
	OriginalDNSDHCPEmpty OriginalDNSKind = "dhcp_empty"
)

// OriginalDNS 兼容两种历史形态：tagged object（v2 正式格式）与
// legacy array（v1 直接保存服务器列表，["Empty"] 表示 DHCP 空配置）。
type OriginalDNS struct {
	Kind    OriginalDNSKind `json:"kind"`
	Servers []string        `json:"servers,omitempty"`
}

func (d *OriginalDNS) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return validationErrorf("empty original_dns")
	}
	switch data[0] {
	case '[':
		var servers []string
		if err := json.Unmarshal(data, &servers); err != nil {
			return syntaxErrorf("decode legacy original_dns: %v", err)
		}
		servers = filterLocalResolvers(servers)
		if len(servers) == 0 || containsString(servers, "Empty") {
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
			return syntaxErrorf("decode tagged original_dns: %v", err)
		}
		kindRaw, ok := raw["kind"]
		if !ok {
			return validationErrorf("original_dns.kind is required")
		}
		var kind OriginalDNSKind
		if err := json.Unmarshal(kindRaw, &kind); err != nil {
			return validationErrorf("decode original_dns.kind: %v", err)
		}
		switch kind {
		case OriginalDNSDHCPEmpty:
			d.Kind = kind
			d.Servers = nil
			return nil
		case OriginalDNSManual:
			serversRaw, ok := raw["servers"]
			if !ok || bytes.Equal(bytes.TrimSpace(serversRaw), []byte("null")) {
				return validationErrorf("original_dns.servers is required for manual DNS")
			}
			var servers []string
			if err := json.Unmarshal(serversRaw, &servers); err != nil {
				return syntaxErrorf("decode original_dns.servers: %v", err)
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
			return validationErrorf("unknown original_dns kind %q", kind)
		}
	default:
		return validationErrorf("original_dns must be a tagged object or legacy array")
	}
}

// Manifest 是 manifest.json 的领域模型。v1 允许缺少 dns_enabled
// 与 original_dns；解析不得为这些字段发明默认值（迁移时再物化）。
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
	if err := decodeModel(data, &decoded, "version", "app_version", "updated_at"); err != nil {
		return err
	}
	*m = Manifest(decoded)
	return nil
}

// RuleSource 标记一条规则来自手动、远程订阅还是广告拦截。
type RuleSource struct {
	Type       string `json:"type"`
	SourceID   string `json:"source_id,omitempty"`
	SourceName string `json:"source_name,omitempty"`
}

// HostRule 是 Profile 内的一条 hosts/DNS 规则；IP 为 nil 表示
// 注释占位规则。
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
	if err := decodeModel(data, &decoded, "id", "domains", "enabled", "source"); err != nil {
		return err
	}
	if !validUUID(decoded.ID) {
		return validationErrorf("invalid rule id %q", decoded.ID)
	}
	switch decoded.Source.Type {
	case "Manual":
	case "Remote", "AdBlock":
		if decoded.Source.SourceID == "" || decoded.Source.SourceName == "" {
			return validationErrorf("remote rule source requires source_id and source_name")
		}
	default:
		return validationErrorf("unknown rule source type %q", decoded.Source.Type)
	}
	*r = HostRule(decoded)
	return nil
}

// Profile 是 profiles/{mode}/<id>.json 的领域模型。mode 缺省为
// hosts（v1 无此字段）；显式 null 视为数据损坏。
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
	if err := decodeModel(data, &decoded, "id", "name", "enabled", "protected", "tags", "rules", "created_at", "updated_at"); err != nil {
		return err
	}
	if !validUUID(decoded.ID) {
		return validationErrorf("invalid profile id %q", decoded.ID)
	}
	if decoded.Mode != ModeHosts && decoded.Mode != ModeDNS {
		return validationErrorf("unknown profile mode %q", decoded.Mode)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return syntaxErrorf("decode profile fields: %v", err)
	}
	if raw, ok := fields["mode"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return validationErrorf("profile mode cannot be null")
	}
	*p = Profile(decoded)
	return nil
}

// SnapshotMeta 是快照索引里的一条记录。
type SnapshotMeta struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  *string   `json:"description"`
	ProfileCount int       `json:"profile_count"`
	CreatedAt    time.Time `json:"created_at"`
}

// SnapshotIndex 是 snapshots/index.json 的领域模型。
type SnapshotIndex struct {
	Version   int            `json:"version"`
	Snapshots []SnapshotMeta `json:"snapshots"`
}

func (i *SnapshotIndex) UnmarshalJSON(data []byte) error {
	type indexAlias SnapshotIndex
	var decoded indexAlias
	if err := decodeModel(data, &decoded, "version", "snapshots"); err != nil {
		return err
	}
	*i = SnapshotIndex(decoded)
	return nil
}

// Snapshot 是 snapshots/<id>.json 的完整内容。
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
	if err := decodeModel(data, &decoded, "id", "name", "profiles", "created_at"); err != nil {
		return err
	}
	*s = Snapshot(decoded)
	return nil
}

// AdBlockSource 是一个广告规则订阅源。
type AdBlockSource struct {
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

func (s *AdBlockSource) UnmarshalJSON(data []byte) error {
	type sourceAlias AdBlockSource
	decoded := sourceAlias{Format: "hosts"}
	if err := decodeModel(data, &decoded, "source_id", "name", "url", "enabled", "response", "rule_count"); err != nil {
		return err
	}
	if decoded.Format != "hosts" && decoded.Format != "domains" {
		return validationErrorf("unknown blocklist format %q", decoded.Format)
	}
	*s = AdBlockSource(decoded)
	return nil
}

// AdBlockState 是 adblock.json 的领域模型。
type AdBlockState struct {
	Enabled              bool            `json:"enabled"`
	Sources              []AdBlockSource `json:"sources"`
	Whitelist            []string        `json:"whitelist"`
	AutoRefreshEnabled   bool            `json:"auto_refresh_enabled"`
	RefreshIntervalHours uint32          `json:"refresh_interval_hours"`
}

func (s *AdBlockState) UnmarshalJSON(data []byte) error {
	type stateAlias AdBlockState
	var decoded stateAlias
	if err := decodeModel(data, &decoded, "enabled", "sources", "whitelist", "auto_refresh_enabled", "refresh_interval_hours"); err != nil {
		return err
	}
	*s = AdBlockState(decoded)
	return nil
}

// Settings 是新版（v2）原生 settings.json 的领域模型。源项目 v1
// 的设置存放在应用内 localStorage，没有磁盘契约；v1→v2 迁移时按
// 默认值物化本文件，不得把 Phase 0 的合成 settings.json 当旧版契约。
type Settings struct {
	QuickApplyOnToggle bool     `json:"quick_apply_on_toggle"`
	PaletteHistory     []string `json:"palette_history"`
}

// DefaultSettings 返回 v2 原生设置的默认值。
func DefaultSettings() Settings {
	return Settings{QuickApplyOnToggle: false, PaletteHistory: []string{}}
}

func (s *Settings) UnmarshalJSON(data []byte) error {
	// 缺字段按默认值补齐：旧副本可能没有 settings.json，或文件来自
	// 早期版本缺 palette_history。
	type settingsAlias Settings
	decoded := settingsAlias(DefaultSettings())
	if err := json.Unmarshal(data, &decoded); err != nil {
		var modelErr *ModelError
		if errors.As(err, &modelErr) {
			return err
		}
		return syntaxErrorf("decode settings: %v", err)
	}
	*s = Settings(decoded)
	return nil
}

// unmarshalModel 统一模型解析入口：保持 *ModelError / *json.SyntaxError
// 原样透出，方便 ClassifyModelError 归类。
func unmarshalModel(data []byte, target any) error {
	return json.Unmarshal(data, target)
}

func marshalModel(value any) ([]byte, error) {
	return json.MarshalIndent(value, "", "  ")
}

// ClassifyModelError 把模型解析错误归类为语法错误或校验错误。
// json.Unmarshal 在调用自定义 UnmarshalJSON 之前会先做一次全文档
// 合法性检查，因此语法错误可能以 *json.SyntaxError 形式到达这里。
// 与模型无关的错误（如 OS 权限错误）返回空串，调用方保留原分类。
func ClassifyModelError(err error) ModelErrKind {
	if err == nil {
		return ""
	}
	var modelErr *ModelError
	if errors.As(err, &modelErr) {
		return modelErr.Kind
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return ModelErrSyntax
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return ModelErrValidation
	}
	return ""
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

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
