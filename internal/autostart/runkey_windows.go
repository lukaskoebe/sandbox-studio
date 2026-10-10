package autostart

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

func hostRunKey() RunKey { return userRunKey{} }

// userRunKey is HKEY_CURRENT_USER's Run key, which needs no administrator rights.
type userRunKey struct{}

func (userRunKey) Get(name string) (string, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", false, err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (userRunKey) Set(name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

func (userRunKey) Delete(name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
