package paths

import (
	"path/filepath"
	"strings"
	"testing"
)

// env is a getenv over a map, so no test reads the real environment.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	cases := []struct {
		goos string
		env  map[string]string
		home string
	}{
		{"darwin", map[string]string{"HOME": "/u"}, "/u/Library/ezc"},
		{"linux", map[string]string{"HOME": "/u"}, "/u/.local/share/ezc"},
		{"linux", map[string]string{"HOME": "/u", "XDG_DATA_HOME": "/x"}, "/x/ezc"},
		{"darwin", map[string]string{"HOME": "/u", "XDG_DATA_HOME": "/x"}, "/x/ezc"},
		{"windows", map[string]string{"USERPROFILE": `C:\u`, "LOCALAPPDATA": `C:\l`}, filepath.Join(`C:\l`, "ezc")},
		{"windows", map[string]string{"USERPROFILE": `C:\u`}, filepath.Join(`C:\u`, "AppData", "Local", "ezc")},
		{"linux", map[string]string{"HOME": "/u", "EZC_HOME": "/e"}, "/e"},
	}
	for _, c := range cases {
		p, err := Resolve(env(c.env), c.goos)
		if err != nil {
			t.Fatalf("%s %v: %v", c.goos, c.env, err)
		}
		if p.Home != c.home {
			t.Errorf("%s %v: Home = %q, want %q", c.goos, c.env, p.Home, c.home)
		}
	}
}

func TestStore(t *testing.T) {
	p, err := Resolve(env(map[string]string{"EZC_HOME": "/e"}), "linux")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Store(); got != "/e/store" {
		t.Errorf("Store() = %q, want /e/store", got)
	}
	if got := p.StoreEzc(); got != "/e/store/.ezc" {
		t.Errorf("StoreEzc() = %q, want /e/store/.ezc", got)
	}
	if got := p.Config(); got != "/e/config.toml" {
		t.Errorf("Config() = %q, want /e/config.toml", got)
	}

	p, err = Resolve(env(map[string]string{"EZC_HOME": "/e", "EZC_STORE": "/s"}), "linux")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Store(); got != "/s" {
		t.Errorf("Store() = %q, want /s", got)
	}
	if got := p.StoreEzc(); got != "/s/.ezc" {
		t.Errorf("StoreEzc() = %q, want /s/.ezc", got)
	}
}

func TestRelativeOverridesRefused(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"HOME": "/u", "EZC_HOME": "rel"}, "EZC_HOME must be an absolute path"},
		{map[string]string{"EZC_HOME": "/e", "EZC_STORE": "rel"}, "EZC_STORE must be an absolute path"},
	}
	for _, c := range cases {
		_, err := Resolve(env(c.env), "linux")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v, want it to contain %q", c.env, err, c.want)
		}
	}
}

func TestNoHome(t *testing.T) {
	_, err := Resolve(env(map[string]string{}), "linux")
	if err == nil || !strings.Contains(err.Error(), "set HOME (or EZC_HOME)") {
		t.Errorf("err = %v, want it to contain %q", err, "set HOME (or EZC_HOME)")
	}

	if _, err := Resolve(env(map[string]string{"EZC_HOME": "/e"}), "linux"); err != nil {
		t.Errorf("EZC_HOME without HOME: %v", err)
	}
}

func TestState(t *testing.T) {
	p, err := Resolve(env(map[string]string{"EZC_HOME": "/e"}), "linux")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/e", "state.json"); p.State() != want {
		t.Errorf("State() = %q, want %q", p.State(), want)
	}
}
