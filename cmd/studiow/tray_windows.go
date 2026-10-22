package main

import (
	"encoding/binary"
	"errors"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A notification-area icon with a context menu, on plain Win32. Its window and message loop
// belong to the thread that called newTray; the menu's actions run on it, so they must hand
// slow work to goroutines.

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procTranslateMessage         = user32.NewProc("TranslateMessage")
	procDispatchMessageW         = user32.NewProc("DispatchMessageW")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procPostQuitMessage          = user32.NewProc("PostQuitMessage")
	procCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	procAppendMenuW              = user32.NewProc("AppendMenuW")
	procDestroyMenu              = user32.NewProc("DestroyMenu")
	procTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procGetCursorPos             = user32.NewProc("GetCursorPos")
	procRegisterWindowMessageW   = user32.NewProc("RegisterWindowMessageW")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procLoadIconW                = user32.NewProc("LoadIconW")
	procMessageBoxW              = user32.NewProc("MessageBoxW")
	procShellNotifyIconW         = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy     = 0x0002
	wmClose       = 0x0010
	wmLButtonUp   = 0x0202
	wmRButtonUp   = 0x0205
	wmContextMenu = 0x007B
	wmApp         = 0x8000
	wmTray        = wmApp + 1 // the icon's mouse events

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x01
	nifIcon    = 0x02
	nifTip     = 0x04
	nifInfo    = 0x10

	niifInfo  = 0x01
	niifError = 0x03

	mfString    = 0x0000
	mfGrayed    = 0x0001
	mfChecked   = 0x0008
	mfSeparator = 0x0800

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100
	tpmNoNotify    = 0x0080

	idiApplication = 32512
	mbIconError    = 0x10
	mbIconInfo     = 0x40
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type point struct{ X, Y int32 }

type winMsg struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

// notifyIconData is NOTIFYICONDATAW, 976 bytes on amd64.
type notifyIconData struct {
	Size            uint32
	Wnd             windows.HWND
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            windows.Handle
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUIDItem        windows.GUID
	BalloonIcon     windows.Handle
}

// menuItem is one entry of the tray's menu; an ID of 0 is a separator.
type menuItem struct {
	ID       int
	Text     string
	Checked  bool
	Disabled bool
}

type tray struct {
	hwnd           windows.HWND
	icon           windows.Handle
	taskbarCreated uint32
	// menu builds the menu each time it opens; choose runs the chosen item, and activate
	// a click on the icon. Both run on the window's thread.
	menu     func() []menuItem
	choose   func(id int)
	activate func()

	mu  sync.Mutex
	tip string
}

// theTray is the one tray the window procedure serves.
var theTray *tray

// newTray creates the hidden window and adds the icon. ico is an .ico file.
func newTray(ico []byte, tip string) (*tray, error) {
	t := &tray{tip: tip}
	theTray = t
	instance, _, _ := procGetModuleHandleW.Call(0)
	class, _ := windows.UTF16PtrFromString("SandboxStudioTray")
	wc := wndClassEx{WndProc: windows.NewCallback(wndProc), Instance: windows.Handle(instance), ClassName: class}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return nil, err
	}
	title, _ := windows.UTF16PtrFromString("Sandbox Studio")
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		return nil, err
	}
	t.hwnd = windows.HWND(hwnd)
	msgName, _ := windows.UTF16PtrFromString("TaskbarCreated")
	r, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(msgName)))
	t.taskbarCreated = uint32(r)
	t.icon = iconFromICO(ico)
	if !t.notifyIcon(nimAdd, nifMessage|nifIcon|nifTip, "", "", 0) {
		return nil, errors.New("the notification area refused the icon")
	}
	return t, nil
}

func (t *tray) notifyIcon(op uintptr, flags uint32, title, text string, infoFlags uint32) bool {
	nid := notifyIconData{Wnd: t.hwnd, ID: 1, Flags: flags, CallbackMessage: wmTray, Icon: t.icon, InfoFlags: infoFlags}
	nid.Size = uint32(unsafe.Sizeof(nid))
	t.mu.Lock()
	copyUTF16(nid.Tip[:], t.tip)
	t.mu.Unlock()
	copyUTF16(nid.InfoTitle[:], title)
	copyUTF16(nid.Info[:], text)
	r, _, _ := procShellNotifyIconW.Call(op, uintptr(unsafe.Pointer(&nid)))
	return r != 0
}

// setTip changes the tooltip; any thread may call it.
func (t *tray) setTip(s string) {
	t.mu.Lock()
	t.tip = s
	t.mu.Unlock()
	t.notifyIcon(nimModify, nifTip|nifIcon, "", "", 0)
}

// balloon shows a notification next to the icon; any thread may call it.
func (t *tray) balloon(title, text string, isError bool) {
	flags := uint32(niifInfo)
	if isError {
		flags = niifError
	}
	t.notifyIcon(nimModify, nifInfo, title, text, flags)
}

// close removes the icon and ends run; any thread may call it.
func (t *tray) close() { _, _, _ = procPostMessageW.Call(uintptr(t.hwnd), wmClose, 0, 0) }

// run is the message loop, until close.
func (t *tray) run() {
	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (t *tray) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer func() { _, _, _ = procDestroyMenu.Call(menu) }()
	for _, it := range t.menu() {
		if it.ID == 0 {
			_, _, _ = procAppendMenuW.Call(menu, mfSeparator, 0, 0)
			continue
		}
		flags := uintptr(mfString)
		if it.Checked {
			flags |= mfChecked
		}
		if it.Disabled {
			flags |= mfGrayed
		}
		text, _ := windows.UTF16PtrFromString(it.Text)
		_, _, _ = procAppendMenuW.Call(menu, flags, uintptr(it.ID), uintptr(unsafe.Pointer(text)))
	}
	var pt point
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// Without the window in the foreground, the menu doesn't close when the user clicks away.
	_, _, _ = procSetForegroundWindow.Call(uintptr(t.hwnd))
	id, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmReturnCmd|tpmNoNotify, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(t.hwnd), 0)
	_, _, _ = procPostMessageW.Call(uintptr(t.hwnd), 0, 0, 0)
	if id != 0 {
		t.choose(int(id))
	}
}

func wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	t := theTray
	switch {
	case t == nil:
	case msg == wmTray:
		switch lparam {
		case wmRButtonUp, wmContextMenu:
			t.showMenu()
		case wmLButtonUp:
			t.activate()
		}
		return 0
	case msg == wmClose:
		t.notifyIcon(nimDelete, 0, "", "", 0)
		_, _, _ = procDestroyWindow.Call(hwnd)
		return 0
	case msg == wmDestroy:
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	case t.taskbarCreated != 0 && uint32(msg) == t.taskbarCreated:
		// Explorer restarted and forgot the icon.
		t.notifyIcon(nimAdd, nifMessage|nifIcon|nifTip, "", "", 0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

// iconFromICO makes an icon of the .ico file's 32×32 image, or its first, falling back on
// the system's application icon.
func iconFromICO(ico []byte) windows.Handle {
	fallback := func() windows.Handle {
		h, _, _ := procLoadIconW.Call(0, idiApplication)
		return windows.Handle(h)
	}
	if len(ico) < 6 || binary.LittleEndian.Uint16(ico[2:]) != 1 {
		return fallback()
	}
	n := int(binary.LittleEndian.Uint16(ico[4:]))
	best := -1
	for i := range n {
		e := 6 + 16*i
		if e+16 > len(ico) {
			break
		}
		if best < 0 || ico[e] == 32 {
			best = i
		}
	}
	if best < 0 {
		return fallback()
	}
	e := 6 + 16*best
	size := binary.LittleEndian.Uint32(ico[e+8:])
	off := binary.LittleEndian.Uint32(ico[e+12:])
	if uint64(off)+uint64(size) > uint64(len(ico)) || size == 0 {
		return fallback()
	}
	w, h := int(ico[e]), int(ico[e+1])
	if w == 0 {
		w = 256
	}
	if h == 0 {
		h = 256
	}
	icon, _, _ := procCreateIconFromResourceEx.Call(uintptr(unsafe.Pointer(&ico[off])), uintptr(size), 1, 0x00030000, uintptr(w), uintptr(h), 0)
	if icon == 0 {
		return fallback()
	}
	return windows.Handle(icon)
}

// copyUTF16 copies s into a fixed, NUL-terminated buffer, cutting it to fit.
func copyUTF16(dst []uint16, s string) {
	u := windows.StringToUTF16(s)
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	copy(dst, u)
}

// messageBox shows a dialog, for errors before the tray exists.
func messageBox(text string, isError bool) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("Sandbox Studio")
	flags := uintptr(mbIconInfo)
	if isError {
		flags = mbIconError
	}
	_, _, _ = procMessageBoxW.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), flags)
}
