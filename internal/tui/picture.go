package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/lukaskoebe/sandbox-studio/internal/browser"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

// Home's detail pane shows the page the sandbox's teammate's browser is on: its address and,
// on terminals with 256 colors or more, a picture of it in half blocks, each cell two pixels of
// the browser's latest frame. The picture is read again every pictureEvery while it shows.

const (
	pictureEvery = 3 * time.Second
	pictureCols  = 40 // the most cells a picture is wide
)

// pictureState is the browser of the teammate whose page the detail pane shows.
type pictureState struct {
	teammate string
	status   browser.BrowserStatus
	img      image.Image // the latest frame, nil while there is none
	loaded   bool
	busy     bool // a read, or the wait for the next one, is under way
	gen      int  // counts the teammates shown, so a read or tick for an earlier one is dropped
	drawn    []string
	drawnW   int // the width the picture was drawn at
}

type (
	pictureLoaded struct {
		gen    int
		status browser.BrowserStatus
		img    image.Image
		err    error
	}
	pictureTick struct{ gen int }
)

// pictureWanted is the teammate whose browser the screen on show draws.
func (m *app) pictureWanted() string {
	if !m.dashShows() {
		return ""
	}
	if sb := m.selected(); sb != nil {
		return sb.TeammateID
	}
	return ""
}

// pictures reports whether the terminal shows the browser's picture.
func (m *app) pictures() bool {
	return m.look.profile >= colorprofile.ANSI256
}

// watchPicture reads the browser of the detail pane's teammate, and its frame when it runs,
// again every pictureEvery while it shows; another teammate's is read at once.
func (m *app) watchPicture() tea.Cmd {
	p := &m.picture
	id := m.pictureWanted()
	if id == "" || (p.busy && id == p.teammate) {
		return nil
	}
	if id != p.teammate {
		*p = pictureState{teammate: id, gen: p.gen + 1}
	}
	p.busy = true
	ctx, client, env, gen, frames := m.ctx, m.client, m.env.ID, p.gen, m.pictures()
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		status, err := client.Browser(rctx, env, id)
		if err != nil || status.VM != string(runtime.StatusRunning) || !frames {
			return pictureLoaded{gen: gen, status: status, err: err}
		}
		var img image.Image
		if data, err := client.BrowserFrame(rctx, env, id); err == nil {
			img, _ = jpeg.Decode(bytes.NewReader(data)) // no picture rather than a broken one
		}
		return pictureLoaded{gen: gen, status: status, img: img}
	}
}

// pictureUpdate applies the picture's messages. ok is false for other messages.
func (m *app) pictureUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	p := &m.picture
	switch msg := msg.(type) {
	case pictureLoaded:
		if msg.gen != p.gen {
			return nil, true
		}
		if msg.err == nil {
			p.status, p.img, p.loaded, p.drawn = msg.status, msg.img, true, nil
		}
		gen := p.gen
		return tea.Tick(pictureEvery, func(time.Time) tea.Msg { return pictureTick{gen} }), true
	case pictureTick:
		if msg.gen == p.gen {
			p.busy = false // watchPicture reads again if the pane still shows
		}
		return nil, true
	}
	return nil, false
}

// browserLines is the BROWSER part of the detail pane in w cells and at most h lines: the page's
// address, then its picture if it fits. Nothing shows for a teammate whose browser isn't
// running.
func (m *app) browserLines(teammate string, w, h int) []string {
	p := &m.picture
	if teammate == "" || p.teammate != teammate || !p.loaded || p.status.VM != string(runtime.StatusRunning) || h < 1 {
		return nil
	}
	label := styleDim.Render(fit("BROWSER", 11))
	page := firstNonEmpty(p.status.URL, "no page yet")
	if p.status.Takeover {
		page += " · you're driving"
	}
	lines := []string{label + clip(page, w-11)}
	if p.img == nil || !m.pictures() {
		return lines
	}
	cols := min(w-11, pictureCols)
	if p.drawn == nil || p.drawnW != cols {
		p.drawn, p.drawnW = halfBlocks(p.img, cols), cols
	}
	if len(p.drawn) > h-1 {
		return lines // a picture cut short shows less than none
	}
	for _, l := range p.drawn {
		lines = append(lines, strings.Repeat(" ", 11)+l)
	}
	return lines
}

// halfBlocks draws an image cols cells wide, keeping its shape: each cell is ▀ in the color of
// its top half over the color of its bottom half, each half the average of the pixels it covers.
func halfBlocks(img image.Image, cols int) []string {
	b := img.Bounds()
	if cols < 1 || b.Dx() < 1 || b.Dy() < 1 {
		return nil
	}
	// A cell is about twice as tall as it is wide, so its two halves are square.
	rows := max((b.Dy()*cols+b.Dx()-1)/b.Dx()/2, 1)
	px := func(x, y int) color.Color {
		x0, x1 := b.Min.X+x*b.Dx()/cols, b.Min.X+(x+1)*b.Dx()/cols
		y0, y1 := b.Min.Y+y*b.Dy()/(2*rows), b.Min.Y+(y+1)*b.Dy()/(2*rows)
		return average(img, max(x1, x0+1), max(y1, y0+1), x0, y0)
	}
	out := make([]string, rows)
	for y := range rows {
		var line strings.Builder
		for x := range cols {
			line.WriteString(lipgloss.NewStyle().Foreground(px(x, 2*y)).Background(px(x, 2*y+1)).Render("▀"))
		}
		out[y] = line.String()
	}
	return out
}

// average is the mean color of the pixels from x0, y0 up to x1, y1, sampling at most 8 by 8.
func average(img image.Image, x1, y1, x0, y0 int) color.Color {
	stepX, stepY := max((x1-x0)/8, 1), max((y1-y0)/8, 1)
	var r, g, bl, n uint32
	for y := y0; y < y1; y += stepY {
		for x := x0; x < x1; x += stepX {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			r, g, bl, n = r+cr>>8, g+cg>>8, bl+cb>>8, n+1
		}
	}
	return color.RGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(bl / n), A: 255}
}
