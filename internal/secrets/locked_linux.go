package secrets

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
)

// KeychainLocked says whether the login keychain is locked, from the Secret Service's login
// collection, without asking it to unlock: reading the key asks with a prompt, and
// gnome-keyring keeps a file open for each prompt it can't show. ok is false when the state
// can't be read, as without a Secret Service.
func KeychainLocked() (locked, ok bool) {
	conn, err := dbus.SessionBus() // shared with go-keyring, and never closed
	if err != nil {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The paths go-keyring reads the key from: the login collection, else the default alias.
	for _, path := range []dbus.ObjectPath{"/org/freedesktop/secrets/collection/login", "/org/freedesktop/secrets/aliases/default"} {
		var v dbus.Variant
		err := conn.Object("org.freedesktop.secrets", path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0,
			"org.freedesktop.Secret.Collection", "Locked").Store(&v)
		if err != nil {
			continue
		}
		if b, isBool := v.Value().(bool); isBool {
			return b, true
		}
	}
	return false, false
}
