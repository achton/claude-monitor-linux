// Package notify sends desktop notifications via the freedesktop
// org.freedesktop.Notifications D-Bus interface (libnotify-compatible).
package notify

import (
	"errors"
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	dbusObject    = "org.freedesktop.Notifications"
	dbusPath      = "/org/freedesktop/Notifications"
	dbusInterface = "org.freedesktop.Notifications.Notify"
)

// Urgency hints — match the freedesktop spec.
type Urgency byte

const (
	UrgencyLow      Urgency = 0
	UrgencyNormal   Urgency = 1
	UrgencyCritical Urgency = 2
)

// Notifier is the live D-Bus connection (lazy).
type Notifier struct {
	mu   sync.Mutex
	conn *dbus.Conn
	err  error
}

// New returns a Notifier. The actual D-Bus connection is opened lazily on first Send.
func New() *Notifier { return &Notifier{} }

// Close closes the underlying connection.
func (n *Notifier) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.conn == nil {
		return nil
	}
	err := n.conn.Close()
	n.conn = nil
	return err
}

// Send fires a notification. AppName is the visible source string ("Claude Monitor").
// Summary is bold; body is normal text. Returns the notification ID assigned by the server.
func (n *Notifier) Send(appName, summary, body string, urgency Urgency) (uint32, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.conn == nil && n.err == nil {
		n.conn, n.err = dbus.SessionBus()
	}
	if n.err != nil {
		return 0, n.err
	}
	if n.conn == nil {
		return 0, errors.New("notify: no session bus")
	}
	hints := map[string]dbus.Variant{
		"urgency": dbus.MakeVariant(byte(urgency)),
	}
	obj := n.conn.Object(dbusObject, dbus.ObjectPath(dbusPath))
	var id uint32
	err := obj.Call(dbusInterface, 0,
		appName,            // app_name
		uint32(0),          // replaces_id
		"claude-monitor",   // app_icon (lookup name)
		summary,            // summary
		body,               // body
		[]string{},         // actions
		hints,              // hints
		int32(-1),          // expire_timeout (-1 = server default)
	).Store(&id)
	if err != nil {
		return 0, fmt.Errorf("dbus Notify: %w", err)
	}
	return id, nil
}
