package tray

import (
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// preferDark returns true if the user prefers a dark color scheme.
// Uses org.freedesktop.portal.Settings (DESIGN.md §5.4); cached for 5 seconds.
func (st *state) preferDark() bool {
	return cachedPreferDark()
}

var (
	darkMu       sync.Mutex
	darkCachedAt time.Time
	darkCached   bool
)

func cachedPreferDark() bool {
	darkMu.Lock()
	defer darkMu.Unlock()
	if time.Since(darkCachedAt) < 5*time.Second {
		return darkCached
	}
	darkCachedAt = time.Now()
	darkCached = queryPortalDark()
	return darkCached
}

func queryPortalDark() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return guessDark()
	}
	obj := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop")
	var v dbus.Variant
	err = obj.Call("org.freedesktop.portal.Settings.Read", 0,
		"org.freedesktop.appearance", "color-scheme",
	).Store(&v)
	if err != nil {
		return guessDark()
	}
	// Settings.Read returns a Variant<Variant<uint32>>; unwrap once.
	if inner, ok := v.Value().(dbus.Variant); ok {
		v = inner
	}
	switch n := v.Value().(type) {
	case uint32:
		return n == 1 // 1 = prefer dark
	case int32:
		return n == 1
	}
	return guessDark()
}

// guessDark is a last-resort heuristic for environments without the portal.
func guessDark() bool {
	if v := strings.ToLower(envOrDefault("GTK_THEME", "")); v != "" {
		return strings.Contains(v, "dark")
	}
	if v := strings.ToLower(envOrDefault("QT_STYLE_OVERRIDE", "")); v != "" {
		return strings.Contains(v, "dark")
	}
	// Reasonable default: most modern Linux DEs run dark themes.
	return true
}

func envOrDefault(key, def string) string {
	v := strings.TrimSpace(envFromOs(key))
	if v == "" {
		return def
	}
	return v
}
