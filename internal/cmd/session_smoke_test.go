package cmd

import (
	"errors"
	"testing"

	configpkg "github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/runtime"
)

func TestCompactSmokeText(t *testing.T) {
	t.Parallel()
	short := compactSmokeText("READY from smoke test")
	if short != "READY from smoke test" {
		t.Fatalf("compactSmokeText(short) = %q", short)
	}
	long := compactSmokeText("one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty")
	if len(long) > 120 {
		t.Fatalf("compactSmokeText(long) len = %d, want <= 120", len(long))
	}
}

func TestFormatSmokeMetadataSortsKeys(t *testing.T) {
	t.Parallel()
	got := formatSmokeMetadata(map[string]string{"b": "2", "a": "1"})
	if got != "a=1, b=2" {
		t.Fatalf("formatSmokeMetadata() = %q, want %q", got, "a=1, b=2")
	}
}

func TestSessionBindingFileNameSanitizesFields(t *testing.T) {
	t.Parallel()
	got := sessionBindingFileName(&runtime.SessionBinding{
		IssueID:   "Smoke/Issue",
		Role:      "Witness",
		RigName:   "Gas Town",
		AgentName: "review@bot",
	})
	if got != "smoke-issue--witness--gas-town--review-bot.json" {
		t.Fatalf("sessionBindingFileName() = %q", got)
	}
}

func TestLoadSmokeRigSettingsReturnsDefaultsWhenMissing(t *testing.T) {
	t.Parallel()

	settings, err := loadSmokeRigSettings("/tmp/does-not-exist/settings/config.json")
	if err != nil {
		t.Fatalf("loadSmokeRigSettings() error = %v", err)
	}
	if settings == nil {
		t.Fatal("loadSmokeRigSettings() returned nil settings")
	}
	if settings.Type != "rig-settings" {
		t.Fatalf("settings.Type = %q, want rig-settings", settings.Type)
	}
}

func TestLoadSmokeRigSettingsPropagatesOtherErrors(t *testing.T) {
	t.Parallel()

	path := t.TempDir()
	_, err := loadSmokeRigSettings(path)
	if err == nil {
		t.Fatal("loadSmokeRigSettings() error = nil, want error")
	}
	if errors.Is(err, configpkg.ErrNotFound) {
		t.Fatalf("loadSmokeRigSettings() error = %v, want non-not-found error", err)
	}
}
