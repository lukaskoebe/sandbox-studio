package tui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"github.com/lukaskoebe/sandbox-studio/internal/browser"
)

// samplePage is a picture of a web page as a browser VM's screencast shows it: a dark header, a
// heading, lines of text and a blue button on white.
func samplePage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1280, 577))
	fill := func(x0, y0, x1, y1 int, c color.Color) {
		draw.Draw(img, image.Rect(x0, y0, x1, y1), image.NewUniform(c), image.Point{}, draw.Src)
	}
	fill(0, 0, 1280, 577, color.White)
	fill(0, 0, 1280, 64, color.RGBA{R: 36, G: 41, B: 47, A: 255})
	fill(96, 120, 700, 170, color.RGBA{R: 30, G: 30, B: 30, A: 255})
	for y := 220; y < 420; y += 40 {
		fill(96, y, 1100, y+14, color.RGBA{R: 180, G: 180, B: 180, A: 255})
	}
	fill(96, 460, 360, 520, color.RGBA{R: 31, G: 111, B: 235, A: 255})
	return img
}

// withBrowser gives api-refactor's teammate a running browser on a page.
func withBrowser(m *app) *app {
	m.picture = pictureState{teammate: "t2", loaded: true, busy: true, img: samplePage(),
		status: browser.BrowserStatus{TeammateID: "t2", VM: "running", URL: "https://github.com/acme/api/pulls"}}
	return m
}

func TestGoldenVisualHomeBrowser(t *testing.T) {
	goldenVisual(t, "visual_home_browser", withBrowser(withDetailData(press(visualApp(140, 40), "j"))), true)
}

// The detail pane reads the selected sandbox's teammate's browser, and its picture only on a
// terminal of 256 colors or more; a picture that doesn't fit shows only the address.
func TestPictureReadsTheSelectedTeammatesBrowser(t *testing.T) {
	m := press(visualApp(140, 40), "j")
	if got := m.pictureWanted(); got != "t2" {
		t.Fatalf("Home reads the selected sandbox's teammate's browser: %q", got)
	}
	if press(visualApp(140, 40), "a").pictureWanted() != "" || visualApp(80, 24).pictureWanted() != "" {
		t.Error("the browser is read off Home, or without the detail pane")
	}
	if cmd := m.watchPicture(); cmd != nil || m.picture.teammate != "t2" || !m.picture.busy {
		t.Fatalf("a second read of the same browser: %v, %+v", cmd != nil, m.picture)
	}
	gen := m.picture.gen
	m.pictureUpdate(pictureLoaded{gen: gen - 1, status: browser.BrowserStatus{VM: "running"}})
	if m.picture.loaded {
		t.Error("a read for the teammate before took effect")
	}
	status := browser.BrowserStatus{VM: "running", URL: "https://example.com/"}
	if cmd, _ := m.pictureUpdate(pictureLoaded{gen: gen, status: status, img: samplePage()}); cmd == nil || !m.picture.loaded {
		t.Fatal("a read shows no browser, or schedules no next read")
	}
	if lines := m.browserLines("t2", 60, 30); len(lines) != 1+9 || !strings.Contains(lines[0], "example.com") {
		t.Errorf("the address and a 40×9 picture: %d lines, %q", len(lines), lines[0])
	}
	if lines := m.browserLines("t2", 60, 5); len(lines) != 1 {
		t.Errorf("a picture that doesn't fit: %d lines", len(lines))
	}
	m.setLook(lookFor(colorprofile.ANSI, false, false))
	if lines := m.browserLines("t2", 60, 30); len(lines) != 1 {
		t.Errorf("a picture in 16 colors: %d lines", len(lines))
	}
	m.picture.status.VM = "stopped"
	if lines := m.browserLines("t2", 60, 30); lines != nil {
		t.Errorf("a stopped browser shows: %q", lines)
	}
}

func TestHalfBlocksKeepTheShape(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 40))
	draw.Draw(img, image.Rect(0, 0, 100, 20), image.NewUniform(color.RGBA{R: 255, A: 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 20, 100, 40), image.NewUniform(color.RGBA{B: 255, A: 255}), image.Point{}, draw.Src)
	lines := halfBlocks(img, 10)
	if len(lines) != 2 { // 10 cells wide, 4 halves tall: 2 rows
		t.Fatalf("%d rows", len(lines))
	}
	if got := strings.Count(lines[0], "▀"); got != 10 {
		t.Errorf("%d cells", got)
	}
	if !strings.Contains(lines[0], "38;2;255;0;0") || !strings.Contains(lines[1], "48;2;0;0;255") {
		t.Errorf("red over red, then blue under blue: %q / %q", lines[0], lines[1])
	}
}

// The client reads a frame's bytes as they come.
func TestBrowserFrameIsTheJPEG(t *testing.T) {
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, samplePage(), nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/environments/e1/teammates/t2/browser/frame" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpg.Bytes())
	}))
	defer srv.Close()
	c := &Client{addr: strings.TrimPrefix(srv.URL, "http://"), http: srv.Client()}
	got, err := c.BrowserFrame(context.Background(), "e1", "t2")
	if err != nil || !bytes.Equal(got, jpg.Bytes()) {
		t.Fatalf("%d bytes, %v", len(got), err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(got)); err != nil {
		t.Error(err)
	}
}
