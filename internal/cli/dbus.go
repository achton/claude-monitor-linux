package cli

import (
	"errors"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	trayBusName   = "org.claude_monitor.Tray"
	trayPath      = "/org/claude_monitor/Tray"
	trayInterface = "org.claude_monitor.Tray"
)

// tryDelegatePoll checks if the tray daemon owns the well-known DBus name.
// If yes, calls Poll(accountID) on it and returns the row count.
// If no, returns an error so the caller falls back to local poll.
func tryDelegatePoll(_ *Env, accountID string) (int, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return 0, err
	}
	// Don't close the shared session bus; just check ownership.
	owners, err := listOwners(conn, trayBusName)
	if err != nil || len(owners) == 0 {
		return 0, errors.New("tray not running")
	}
	obj := conn.Object(trayBusName, dbus.ObjectPath(trayPath))
	call := obj.Call(trayInterface+".Poll", 0, accountID)
	if call.Err != nil {
		return 0, call.Err
	}
	var rows int32
	var errStr string
	if err := call.Store(&rows, &errStr); err != nil {
		return 0, err
	}
	if errStr != "" {
		return int(rows), errors.New(errStr)
	}
	_ = time.Second // (no use; here for future timeouts)
	return int(rows), nil
}

func listOwners(conn *dbus.Conn, name string) ([]string, error) {
	var owners []string
	err := conn.BusObject().Call("org.freedesktop.DBus.ListQueuedOwners", 0, name).Store(&owners)
	return owners, err
}
