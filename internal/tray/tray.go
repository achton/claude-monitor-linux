// Package tray runs Fyne's integrated SNI tray + the polling loop + the DBus
// service that CLI subcommands delegate to.
//
// Imported only from cmd/claude-monitor/tray_entry.go. See DESIGN.md §11.
package tray

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	"github.com/godbus/dbus/v5"

	"github.com/achton/claude-monitor-linux/internal/cli"
	"github.com/achton/claude-monitor-linux/internal/config"
	cmlog "github.com/achton/claude-monitor-linux/internal/log"
	"github.com/achton/claude-monitor-linux/internal/ui"
	"github.com/achton/claude-monitor-linux/internal/xdg"
)

const (
	dbusName      = "org.claude_monitor.Tray"
	dbusPath      = "/org/claude_monitor/Tray"
	dbusInterface = "org.claude_monitor.Tray"

	sniWatcher = "org.kde.StatusNotifierWatcher"
)

// Run launches the tray. Blocks the calling goroutine until the user quits.
func Run(env *cli.Env, cfg config.Config) error {
	conn, busErr := dbus.SessionBus()
	if busErr != nil {
		return fmt.Errorf("no session bus: %w", busErr)
	}
	if !nameHasOwner(conn, sniWatcher) {
		return handleNoSNI(env, conn)
	}

	lock, err := xdg.AcquireLock()
	if err != nil {
		if errors.Is(err, xdg.ErrLocked) {
			if focusErr := callFocus(conn); focusErr == nil {
				return nil
			}
			return fmt.Errorf("another tray instance is running and unreachable via DBus")
		}
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lock.Release()

	reply, err := conn.RequestName(dbusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("dbus request name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("dbus name %s already owned", dbusName)
	}

	a := fyneapp.NewWithID("org.claude_monitor")
	desk, ok := a.(desktop.App)
	if !ok {
		return errors.New("system tray is not supported by this Fyne driver")
	}

	state := newState(env, a, desk, cfg)

	// Initial tray icon + menu (before any polling has happened).
	state.refreshIcon()
	state.rebuildMenu()

	// Register the DBus surface.
	svc := &dbusService{state: state}
	if err := conn.Export(svc, dbusPath, dbusInterface); err != nil {
		return fmt.Errorf("dbus export: %w", err)
	}

	// Start the polling goroutine.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go state.pollLoop(ctx)

	// Signal handler.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cmlog.Logger().Info("tray: signal received, shutting down")
		a.Quit()
	}()

	// Block on Fyne's main event loop. This handles both the tray menu/icon
	// (via the desktop.App integration) and any windows opened by the menu.
	a.Run()
	return nil
}

func nameHasOwner(conn *dbus.Conn, name string) bool {
	var has bool
	err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, name).Store(&has)
	if err != nil {
		return false
	}
	return has
}

func callFocus(conn *dbus.Conn) error {
	obj := conn.Object(dbusName, dbus.ObjectPath(dbusPath))
	return obj.Call(dbusInterface+".Focus", 0).Err
}

// handleNoSNI implements the §5.4 / §11 fallback policy: a one-shot
// notification + non-zero exit when the tray was explicitly requested but no
// SNI host is present.
func handleNoSNI(env *cli.Env, conn *dbus.Conn) error {
	msg := `No system tray host detected on this session bus.
On GNOME, install the AppIndicator and KStatusNotifierItem Support extension.
On Sway/i3/Hyprland, ensure your bar (Waybar, etc.) supports the StatusNotifierItem spec.
To use claude-monitor without a tray, use the CLI subcommands directly
(claude-monitor status, claude-monitor poll, etc.).`

	if conn != nil {
		obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
		hints := map[string]dbus.Variant{"urgency": dbus.MakeVariant(byte(1))}
		_ = obj.Call("org.freedesktop.Notifications.Notify", 0,
			"Claude Monitor", uint32(0), "claude-monitor",
			"Tray unavailable", msg, []string{},
			hints, int32(-1)).Err
	}
	if env != nil && env.Stderr != nil {
		fmt.Fprintln(env.Stderr, msg)
	}
	return errors.New("no SNI watcher available")
}

// ----- DBus service -----

type dbusService struct{ state *state }

func (s *dbusService) Focus() *dbus.Error {
	s.state.focusAccountList()
	return nil
}

func (s *dbusService) Poll(accountID string) (int32, string, *dbus.Error) {
	cmlog.Logger().Info("dbus Poll requested", slog.String("account", accountID))
	if accountID == "" {
		n, err := s.state.env.Poller.PollAll(s.state.ctx)
		if err != nil {
			return int32(n), err.Error(), nil
		}
		return int32(n), "", nil
	}
	if err := s.state.env.Poller.PollAccount(s.state.ctx, accountID); err != nil {
		return 0, err.Error(), nil
	}
	return 1, "", nil
}

func (s *dbusService) Probe(accountID string) (string, string, *dbus.Error) {
	creds, err := s.state.env.Store.ListActiveCredentials(s.state.ctx)
	if err != nil {
		return "", err.Error(), nil
	}
	for _, c := range creds {
		if c.AccountID.Valid && c.AccountID.String == accountID {
			if err := s.state.env.Store.ResetCredentialEndpointState(s.state.ctx, c.ID); err != nil {
				return c.UsageEndpointState, err.Error(), nil
			}
			return "healthy", "", nil
		}
	}
	return "", "no credential for account", nil
}

// ----- shared tray state -----

type state struct {
	env  *cli.Env
	app  fyne.App
	desk desktop.App
	cfg  config.Config
	ctx  context.Context

	winMu             sync.Mutex
	accountListWindow fyne.Window
}

func newState(env *cli.Env, app fyne.App, desk desktop.App, cfg config.Config) *state {
	return &state{env: env, app: app, desk: desk, cfg: cfg, ctx: env.Ctx}
}

func (st *state) pollLoop(ctx context.Context) {
	// Start the fsnotify watcher on Claude Code's credentials file. Silently
	// skips when Claude Code isn't installed; paste-only users see the same
	// behavior as before.
	st.env.Poller.StartCredentialsWatcher(ctx)

	if _, err := st.env.Poller.PollAll(ctx); err != nil {
		cmlog.Logger().Warn("initial poll", slog.String("err", err.Error()))
	}
	st.refreshIcon()
	st.rebuildMenu()

	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if _, err := st.env.Poller.PollDue(ctx); err != nil {
				cmlog.Logger().Warn("pollDue", slog.String("err", err.Error()))
			}
			st.refreshIcon()
			st.rebuildMenu()
		}
	}
}

func (st *state) focusAccountList() {
	st.winMu.Lock()
	w := st.accountListWindow
	st.winMu.Unlock()
	if w != nil {
		w.Show()
		w.RequestFocus()
		return
	}
	nw := ui.NewAccountListWindow(st.app, st.env)
	st.winMu.Lock()
	st.accountListWindow = nw
	st.winMu.Unlock()
	nw.SetOnClosed(func() {
		st.winMu.Lock()
		st.accountListWindow = nil
		st.winMu.Unlock()
	})
	nw.Show()
}

// openAddAccount opens the add-account window directly from the tray menu.
func (st *state) openAddAccount() {
	w := ui.NewAddAccountWindow(st.app, st.env, func() {
		st.winMu.Lock()
		if st.accountListWindow != nil {
			st.accountListWindow.Content().Refresh()
		}
		st.winMu.Unlock()
		st.rebuildMenu()
		st.refreshIcon()
	})
	w.Show()
}
