//go:build !linux

package secrets

// KeychainLocked says whether the login keychain is locked, without asking it to unlock. Only
// Linux's Secret Service tells; elsewhere ok is false.
func KeychainLocked() (locked, ok bool) { return false, false }
