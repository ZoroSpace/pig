// pi: test/settings-manager-compaction.test.ts (new in v0.86.0) — compaction
// model overrides (#8133) + the retry/cacheWarming settings additions from
// test/settings-manager.test.ts.
package core

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

var compactionOverrideTestModel = &types.Model{Provider: types.ProviderID("provider"), ID: "family/model"}

func compactionTestModel(provider, id string) *types.Model {
	return &types.Model{Provider: types.ProviderID(provider), ID: id}
}

func mustGetCompactionSettings(t *testing.T, manager *SettingsManager, model *types.Model) CompactionSettingsValues {
	t.Helper()
	settings, err := manager.GetCompactionSettings(model)
	if err != nil {
		t.Fatalf("GetCompactionSettings returned error: %v", err)
	}
	return settings
}

func TestCompactionModelOverridesDefaults(t *testing.T) {
	manager := InMemorySettingsManager(nil, nil)
	defaults := CompactionSettingsValues{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}
	if got := mustGetCompactionSettings(t, manager, nil); got != defaults {
		t.Fatalf("defaults = %+v", got)
	}
	if got := mustGetCompactionSettings(t, manager, compactionOverrideTestModel); got != defaults {
		t.Fatalf("model defaults = %+v", got)
	}
}

func TestCompactionModelOverridesResolveFieldsIndependently(t *testing.T) {
	manager := InMemorySettingsManager(Settings{
		"compaction": Settings{
			"reserveTokens":    float64(8192),
			"keepRecentTokens": float64(10000),
			"modelOverrides": map[string]any{
				"provider/family/model": map[string]any{"reserveTokens": float64(400000)},
			},
		},
	}, nil)
	got := mustGetCompactionSettings(t, manager, compactionOverrideTestModel)
	want := CompactionSettingsValues{Enabled: true, ReserveTokens: 400000, KeepRecentTokens: 10000}
	if got != want {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
	reserve, err := manager.GetCompactionReserveTokens(compactionOverrideTestModel)
	if err != nil || reserve != 400000 {
		t.Fatalf("reserve = %d, %v", reserve, err)
	}
	keep, err := manager.GetCompactionKeepRecentTokens(compactionOverrideTestModel)
	if err != nil || keep != 10000 {
		t.Fatalf("keep = %d, %v", keep, err)
	}
	if got := mustGetCompactionSettings(t, manager, nil); got != (CompactionSettingsValues{Enabled: true, ReserveTokens: 8192, KeepRecentTokens: 10000}) {
		t.Fatalf("ordinary settings = %+v", got)
	}

	manager.ApplyOverrides(Settings{
		"compaction": Settings{
			"modelOverrides": map[string]any{
				"provider/family/model": map[string]any{"keepRecentTokens": float64(30000)},
			},
		},
	})
	keep, err = manager.GetCompactionKeepRecentTokens(compactionOverrideTestModel)
	if err != nil || keep != 30000 {
		t.Fatalf("keep after override = %d, %v", keep, err)
	}
	reserve, err = manager.GetCompactionReserveTokens(compactionOverrideTestModel)
	if err != nil || reserve != 400000 {
		t.Fatalf("reserve after override = %d, %v", reserve, err)
	}
}

func TestCompactionModelOverridesFallbackToDefaults(t *testing.T) {
	manager := InMemorySettingsManager(Settings{
		"compaction": Settings{
			"modelOverrides": map[string]any{
				"provider/family/model": map[string]any{"keepRecentTokens": float64(1024)},
			},
		},
	}, nil)
	got := mustGetCompactionSettings(t, manager, compactionOverrideTestModel)
	if got != (CompactionSettingsValues{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 1024}) {
		t.Fatalf("settings = %+v", got)
	}
}

func TestCompactionModelOverridesMatchExactIDsIncludingSlashes(t *testing.T) {
	manager := InMemorySettingsManager(Settings{
		"compaction": Settings{
			"modelOverrides": map[string]any{
				"provider/family/model": map[string]any{"reserveTokens": float64(400000)},
				"provider/*":            map[string]any{"reserveTokens": float64(1)},
				"family/model":          map[string]any{"reserveTokens": float64(2)},
			},
		},
	}, nil)
	reserve, err := manager.GetCompactionReserveTokens(compactionOverrideTestModel)
	if err != nil || reserve != 400000 {
		t.Fatalf("reserve = %d, %v", reserve, err)
	}
	defaults := CompactionSettingsValues{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}
	for _, other := range []struct{ provider, id string }{
		{"other", compactionOverrideTestModel.ID},
		{string(compactionOverrideTestModel.Provider), "other"},
		{string(compactionOverrideTestModel.Provider), "family/Model"},
	} {
		if got := mustGetCompactionSettings(t, manager, compactionTestModel(other.provider, other.id)); got != defaults {
			t.Fatalf("settings for %s/%s = %+v, want defaults", other.provider, other.id, got)
		}
	}
}

func TestCompactionModelOverridesMergeProjectLayer(t *testing.T) {
	storage := &InMemorySettingsStorage{}
	mustWithLock(t, storage, SettingsScopeGlobal, `{
		"compaction": {
			"reserveTokens": 8192,
			"modelOverrides": {
				"provider/family/model": {"reserveTokens": 400000, "keepRecentTokens": 30000},
				"provider/other": {"keepRecentTokens": 4096}
			}
		}
	}`)
	mustWithLock(t, storage, SettingsScopeProject, `{
		"compaction": {
			"reserveTokens": 1024,
			"modelOverrides": {"provider/family/model": {"keepRecentTokens": 2000}}
		}
	}`)
	manager := SettingsManagerFromStorage(storage, nil)
	got := mustGetCompactionSettings(t, manager, compactionOverrideTestModel)
	want := CompactionSettingsValues{Enabled: true, ReserveTokens: 400000, KeepRecentTokens: 2000}
	if got != want {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
	other := mustGetCompactionSettings(t, manager, compactionTestModel("provider", "other"))
	if other != (CompactionSettingsValues{Enabled: true, ReserveTokens: 1024, KeepRecentTokens: 4096}) {
		t.Fatalf("other settings = %+v", other)
	}
	manager.Reload()
	keep, err := manager.GetCompactionKeepRecentTokens(compactionOverrideTestModel)
	if err != nil || keep != 2000 {
		t.Fatalf("keep after reload = %d, %v", keep, err)
	}
	manager.SetProjectTrusted(false)
	keep, err = manager.GetCompactionKeepRecentTokens(compactionOverrideTestModel)
	if err != nil || keep != 30000 {
		t.Fatalf("keep without project trust = %d, %v", keep, err)
	}
}

func mustWithLock(t *testing.T, storage *InMemorySettingsStorage, scope SettingsScope, payload string) {
	t.Helper()
	if err := storage.WithLock(scope, func(current *string) *string { return &payload }); err != nil {
		t.Fatalf("WithLock(%s) failed: %v", scope, err)
	}
}

func TestCompactionModelOverridesEnabledStaysGlobal(t *testing.T) {
	storage := &InMemorySettingsStorage{}
	mustWithLock(t, storage, SettingsScopeGlobal, `{
		"compaction": {"modelOverrides": {"provider/family/model": {"enabled": false, "reserveTokens": 400000}}}
	}`)
	manager := SettingsManagerFromStorage(storage, nil)
	if got := mustGetCompactionSettings(t, manager, compactionOverrideTestModel); got.Enabled != true {
		t.Fatalf("enabled = %v, want true (overrides keep enabled global)", got.Enabled)
	}
	manager.SetCompactionEnabled(false)
	manager.Flush()
	manager.Reload()
	got := mustGetCompactionSettings(t, manager, compactionOverrideTestModel)
	want := CompactionSettingsValues{Enabled: false, ReserveTokens: 16384, KeepRecentTokens: 20000}
	// The model override keeps reserveTokens; the toggle persists globally.
	want.ReserveTokens = 400000
	if got != want {
		t.Fatalf("settings after toggle = %+v, want %+v", got, want)
	}
}

func TestCompactionModelOverrideInvalidTokenValues(t *testing.T) {
	for _, field := range []string{"reserveTokens", "keepRecentTokens"} {
		for _, value := range []any{nil, float64(-1), 1.5, "400000", true, map[string]any{}, []any{}, float64(9007199254740993)} {
			storage := &InMemorySettingsStorage{}
			entry := map[string]any{field: value}
			payload, err := json.Marshal(map[string]any{
				"compaction": map[string]any{
					"modelOverrides": map[string]any{"provider/family/model": entry},
				},
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			mustWithLock(t, storage, SettingsScopeGlobal, string(payload))
			manager := SettingsManagerFromStorage(storage, nil)
			_, err = manager.GetCompactionSettings(compactionOverrideTestModel)
			if err == nil {
				t.Fatalf("field %s value %#v: expected error", field, value)
			}
			want := `Invalid compaction.modelOverrides["provider/family/model"].` + field + ` setting: ` + formatSettingValue(value) + `. Expected a non-negative safe integer.`
			if err.Error() != want {
				t.Fatalf("field %s value %#v:\n got %q\nwant %q", field, value, err.Error(), want)
			}
			// Other models and the ordinary settings stay valid.
			if got := mustGetCompactionSettings(t, manager, nil); got != (CompactionSettingsValues{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}) {
				t.Fatalf("ordinary settings = %+v", got)
			}
			if got := mustGetCompactionSettings(t, manager, compactionTestModel("other", compactionOverrideTestModel.ID)); got != (CompactionSettingsValues{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}) {
				t.Fatalf("other model settings = %+v", got)
			}
		}
	}
}

func TestCompactionModelOverrideNonFiniteRuntimeValues(t *testing.T) {
	for _, field := range []string{"reserveTokens", "keepRecentTokens"} {
		for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			manager := InMemorySettingsManager(nil, nil)
			manager.ApplyOverrides(Settings{
				"compaction": Settings{
					"modelOverrides": map[string]any{
						"provider/family/model": map[string]any{field: value},
					},
				},
			})
			_, err := manager.GetCompactionSettings(compactionOverrideTestModel)
			if err == nil {
				t.Fatalf("field %s value %v: expected error", field, value)
			}
			want := `Invalid compaction.modelOverrides["provider/family/model"].` + field + ` setting: ` + formatSettingValue(value)
			if !strings.HasPrefix(err.Error(), want) {
				t.Fatalf("field %s value %v:\n got %q\nwant prefix %q", field, value, err.Error(), want)
			}
		}
	}
}

func TestCompactionOrdinaryInvalidValuesEvenWithValidOverride(t *testing.T) {
	for _, field := range []string{"reserveTokens", "keepRecentTokens"} {
		for _, value := range []any{nil, float64(-1), 1.5, "400000", true, map[string]any{}, []any{}, float64(9007199254740993)} {
			storage := &InMemorySettingsStorage{}
			payload, err := json.Marshal(map[string]any{
				"compaction": map[string]any{
					field:            value,
					"modelOverrides": map[string]any{"provider/family/model": map[string]any{field: float64(4096)}},
				},
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			mustWithLock(t, storage, SettingsScopeGlobal, string(payload))
			manager := SettingsManagerFromStorage(storage, nil)
			want := `Invalid compaction.` + field + ` setting: ` + formatSettingValue(value) + `. Expected a non-negative safe integer.`
			if _, err := manager.GetCompactionSettings(nil); err == nil || err.Error() != want {
				t.Fatalf("field %s value %#v: got %v, want %q", field, value, err, want)
			}
			if _, err := manager.GetCompactionSettings(compactionOverrideTestModel); err == nil || err.Error() != want {
				t.Fatalf("field %s value %#v (with model): got %v, want %q", field, value, err, want)
			}
		}
	}
}

func TestCompactionOrdinaryNonFiniteRuntimeValues(t *testing.T) {
	for _, field := range []string{"reserveTokens", "keepRecentTokens"} {
		for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			manager := InMemorySettingsManager(nil, nil)
			manager.ApplyOverrides(Settings{"compaction": Settings{field: value}})
			_, err := manager.GetCompactionSettings(nil)
			if err == nil {
				t.Fatalf("field %s value %v: expected error", field, value)
			}
			want := `Invalid compaction.` + field + ` setting: ` + formatSettingValue(value)
			if !strings.HasPrefix(err.Error(), want) {
				t.Fatalf("field %s value %v:\n got %q\nwant prefix %q", field, value, err.Error(), want)
			}
		}
	}
}

func TestCompactionMalformedModelEntries(t *testing.T) {
	for _, entry := range []any{nil, false, float64(42), "invalid", []any{}} {
		storage := &InMemorySettingsStorage{}
		payload, err := json.Marshal(map[string]any{
			"compaction": map[string]any{
				"modelOverrides": map[string]any{"provider/family/model": entry},
			},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		mustWithLock(t, storage, SettingsScopeGlobal, string(payload))
		manager := SettingsManagerFromStorage(storage, nil)
		_, getErr := manager.GetCompactionSettings(compactionOverrideTestModel)
		if getErr == nil {
			t.Fatalf("entry %#v: expected error", entry)
		}
		want := `Invalid compaction.modelOverrides["provider/family/model"] setting: ` + formatSettingValue(entry) + `. Expected an object.`
		if getErr.Error() != want {
			t.Fatalf("entry %#v:\n got %q\nwant %q", entry, getErr.Error(), want)
		}
	}
}

func TestCompactionAcceptsZero(t *testing.T) {
	manager := InMemorySettingsManager(Settings{
		"compaction": Settings{"reserveTokens": float64(0), "keepRecentTokens": float64(0)},
	}, nil)
	if got := mustGetCompactionSettings(t, manager, compactionOverrideTestModel); got != (CompactionSettingsValues{Enabled: true, ReserveTokens: 0, KeepRecentTokens: 0}) {
		t.Fatalf("zero settings = %+v", got)
	}
	manager.ApplyOverrides(Settings{
		"compaction": Settings{
			"reserveTokens":    float64(1000),
			"keepRecentTokens": float64(1000),
			"modelOverrides": map[string]any{
				"provider/family/model": map[string]any{"reserveTokens": float64(0), "keepRecentTokens": float64(0)},
			},
		},
	})
	if got := mustGetCompactionSettings(t, manager, compactionOverrideTestModel); got != (CompactionSettingsValues{Enabled: true, ReserveTokens: 0, KeepRecentTokens: 0}) {
		t.Fatalf("zero overrides = %+v", got)
	}
}

// The retry/cacheWarming additions from test/settings-manager.test.ts.

func TestRetrySettingsAgentDelayCap(t *testing.T) {
	manager := InMemorySettingsManager(nil, nil)
	settings := manager.GetRetrySettings()
	if settings != (RetrySettingsValues{Enabled: true, MaxRetries: 3, BaseDelayMs: 2000, MaxAgentDelayMs: 60000}) {
		t.Fatalf("defaults = %+v", settings)
	}
	override := InMemorySettingsManager(Settings{
		"retry": Settings{"enabled": true, "maxRetries": float64(10), "baseDelayMs": float64(500), "maxAgentDelayMs": float64(5000)},
	}, nil)
	if override.GetRetrySettings() != (RetrySettingsValues{Enabled: true, MaxRetries: 10, BaseDelayMs: 500, MaxAgentDelayMs: 5000}) {
		t.Fatalf("override = %+v", override.GetRetrySettings())
	}
}

func TestCacheWarmingModeDefaultsAndLayers(t *testing.T) {
	t.Run("defaults to streaming and ignores project settings", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		if got := CreateSettingsManager(projectDir, agentDir, nil).GetCacheWarmingMode(); got != CacheWarmingMode("streaming") {
			t.Fatalf("default = %q", got)
		}
		writeSettingsFile(t, filepath.Join(projectDir, ".pig", "settings.json"), `{"cacheWarming":"idle"}`)
		if got := CreateSettingsManager(projectDir, agentDir, nil).GetCacheWarmingMode(); got != CacheWarmingMode("streaming") {
			t.Fatalf("project layer = %q, want streaming (global only)", got)
		}
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"cacheWarming":"idle"}`)
		if got := CreateSettingsManager(projectDir, agentDir, nil).GetCacheWarmingMode(); got != CacheWarmingMode("idle") {
			t.Fatalf("global layer = %q, want idle", got)
		}
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"cacheWarming":"bogus"}`)
		if got := CreateSettingsManager(projectDir, agentDir, nil).GetCacheWarmingMode(); got != CacheWarmingMode("streaming") {
			t.Fatalf("bogus = %q, want streaming", got)
		}
	})

	t.Run("persists the mode globally", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		manager.SetCacheWarmingMode(CacheWarmingMode("off"))
		manager.Flush()

		if got := CreateSettingsManager(projectDir, agentDir, nil).GetCacheWarmingMode(); got != CacheWarmingMode("off") {
			t.Fatalf("persisted = %q, want off", got)
		}
		saved := readSettingsJSON(t, filepath.Join(agentDir, "settings.json"))
		if got := settingsJSONString(t, saved, "cacheWarming"); got != "off" {
			t.Fatalf("cacheWarming = %q, want off", got)
		}
	})
}
