package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	configpkg "github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/runtime"
)

type smokeManagedSessionStub struct{}

func (smokeManagedSessionStub) ID() string { return "runtime-xyz" }
func (smokeManagedSessionStub) Status(_ context.Context) (runtime.SessionStatus, error) {
	return runtime.SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: true}, nil
}
func (smokeManagedSessionStub) Send(_ context.Context, _ string) error { return nil }
func (smokeManagedSessionStub) Close(_ context.Context) error          { return nil }

type smokeLookupStub struct {
	session runtime.ManagedSession
	err     error
}

func (s smokeLookupStub) Lookup(_ context.Context, _ runtime.SessionLookupRequest) (runtime.ManagedSession, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.session, nil
}

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

func TestCompactSmokeTextNormalizesWhitespace(t *testing.T) {
	t.Parallel()
	got := compactSmokeText("READY\n\n from\t smoke   test")
	if got != "READY from smoke test" {
		t.Fatalf("compactSmokeText() = %q, want normalized whitespace", got)
	}
}

func TestFormatSmokeMetadataIncludesAllKeys(t *testing.T) {
	t.Parallel()
	got := formatSmokeMetadata(map[string]string{"cli_url": "http://127.0.0.1:4321", "external_server": "true"})
	for _, want := range []string{"cli_url=http://127.0.0.1:4321", "external_server=true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatSmokeMetadata() = %q, want substring %q", got, want)
		}
	}
}

func TestResumeSmokeSessionFallsBackToLiveManagedSessionOnAuthResumeError(t *testing.T) {
	t.Parallel()
	live := smokeManagedSessionStub{}
	got, viaLookup, err := resumeSmokeSession(context.Background(), smokeLookupStub{err: fmt.Errorf("JSON-RPC Error -32603: Request session.resume failed with message: No authentication info available")}, runtime.SessionLookupRequest{}, live)
	if err != nil {
		t.Fatalf("resumeSmokeSession() error = %v", err)
	}
	if viaLookup {
		t.Fatal("resumeSmokeSession() viaLookup = true, want false fallback")
	}
	if got.ID() != live.ID() {
		t.Fatalf("resumeSmokeSession() session id = %q, want %q", got.ID(), live.ID())
	}
}

func TestResumeSmokeSessionReturnsLookupSessionWhenAvailable(t *testing.T) {
	t.Parallel()
	lookedUp := smokeManagedSessionStub{}
	got, viaLookup, err := resumeSmokeSession(context.Background(), smokeLookupStub{session: lookedUp}, runtime.SessionLookupRequest{}, nil)
	if err != nil {
		t.Fatalf("resumeSmokeSession() error = %v", err)
	}
	if !viaLookup {
		t.Fatal("resumeSmokeSession() viaLookup = false, want true")
	}
	if got.ID() != lookedUp.ID() {
		t.Fatalf("resumeSmokeSession() session id = %q, want %q", got.ID(), lookedUp.ID())
	}
}
