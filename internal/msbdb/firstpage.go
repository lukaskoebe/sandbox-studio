package msbdb

import (
	"encoding/binary"
	"errors"
	"os"
	"slices"
)

// A catalog's first page holds SQLite's header and the root of sqlite_master, the list of its
// tables. When that page is damaged SQLite can't read the file at all ("file is not a
// database"), though the rest of the list, and every table, may be intact on later pages.
// rebuildFirstPage writes a new header and a new root that points at the pages of the list it
// finds, so that salvage can read the file again. See https://www.sqlite.org/fileformat.html.

// errNoSchemaPages is a file in which no page of sqlite_master was found.
var errNoSchemaPages = errors.New("no page of its list of tables was found")

// pageSizes are the page sizes tried when the header can't tell, most likely first.
var pageSizes = []int{4096, 1024, 2048, 8192, 16384, 32768, 65536, 512}

// schemaLeaf is a leaf page of sqlite_master: its page number and the rowids of its rows.
type schemaLeaf struct {
	page     int
	min, max int64
	roots    []int64 // the root pages its rows name
}

// rebuildFirstPage rewrites the first page of the database file at path in place, as a
// database in WAL mode when wal is set (so SQLite reads the -wal file beside it, which may hold
// a sound copy of the first page) and as one without otherwise. Call it on a copy: it changes
// the file.
func rebuildFirstPage(path string, wal bool) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sizes := pageSizes
	reserved := 0
	if string(b[:min(len(b), 16)]) == "SQLite format 3\x00" && len(b) >= 100 {
		// The header may still name the page size even though the page is damaged.
		ps := int(binary.BigEndian.Uint16(b[16:18]))
		if ps == 1 {
			ps = 65536
		}
		if ps >= 512 && ps&(ps-1) == 0 {
			sizes = append([]int{ps}, sizes...)
		}
		reserved = int(b[20])
	}
	// A header that still reads may name a wrong reserved size too, so none is tried as well.
	best, bestSize, bestReserved := []schemaLeaf(nil), 0, 0
	for _, res := range slices.Compact([]int{reserved, 0}) {
		for _, ps := range sizes {
			if len(b) < 2*ps || ps-res < 480 {
				continue
			}
			if leaves := schemaLeaves(b, ps, ps-res); len(leaves) > len(best) {
				best, bestSize, bestReserved = leaves, ps, res
			}
		}
	}
	if len(best) == 0 {
		return errNoSchemaPages
	}
	return writeFirstPage(path, b, best, bestSize, bestReserved, wal)
}

// schemaLeaves finds the leaf pages of sqlite_master in b, its pages ps bytes long with usable
// bytes each. Freed pages may still hold an older copy of the list; of pages whose rowids
// overlap, those whose rows all name valid root pages and then those with more rows win.
func schemaLeaves(b []byte, ps, usable int) []schemaLeaf {
	pages := len(b) / ps
	var found []schemaLeaf
	for p := 2; p <= pages; p++ {
		if l, ok := schemaLeafAt(b, p, ps, usable); ok {
			found = append(found, l)
		}
	}
	valid := func(l schemaLeaf) bool {
		for _, r := range l.roots {
			if r < 1 || int(r) > pages {
				return false
			}
			switch b[(int(r)-1)*ps] {
			case 0x02, 0x05, 0x0a, 0x0d:
			default:
				return false
			}
		}
		return true
	}
	slices.SortStableFunc(found, func(a, c schemaLeaf) int {
		if va, vc := valid(a), valid(c); va != vc {
			if va {
				return -1
			}
			return 1
		}
		return int((c.max - c.min) - (a.max - a.min))
	})
	var kept []schemaLeaf
	for _, l := range found {
		if !slices.ContainsFunc(kept, func(k schemaLeaf) bool { return l.min <= k.max && k.min <= l.max }) {
			kept = append(kept, l)
		}
	}
	slices.SortFunc(kept, func(a, c schemaLeaf) int { return int(a.min - c.min) })
	return kept
}

// schemaLeafAt reads page p as a leaf of sqlite_master: a table leaf whose every row has
// sqlite_master's five columns, the first being "table", "index", "view" or "trigger".
func schemaLeafAt(b []byte, p, ps, usable int) (schemaLeaf, bool) {
	page := b[(p-1)*ps : p*ps]
	if page[0] != 0x0d {
		return schemaLeaf{}, false
	}
	n := int(binary.BigEndian.Uint16(page[3:5]))
	if n == 0 || 8+2*n > usable {
		return schemaLeaf{}, false
	}
	l := schemaLeaf{page: p}
	for i := range n {
		at := int(binary.BigEndian.Uint16(page[8+2*i:]))
		if at < 8+2*n || at >= usable {
			return schemaLeaf{}, false
		}
		size, k := varint(page[at:usable])
		if k == 0 {
			return schemaLeaf{}, false
		}
		rowid, k2 := varint(page[at+k : usable])
		if k2 == 0 {
			return schemaLeaf{}, false
		}
		// Only the part of the row on this page is read; the record's header and the first
		// columns are near its start.
		local := localPayload(size, usable)
		start := at + k + k2
		rec := page[start:min(usable, start+local)]
		root, ok := schemaRow(rec)
		if !ok {
			return schemaLeaf{}, false
		}
		if i == 0 || int64(rowid) < l.min {
			l.min = int64(rowid)
		}
		if i == 0 || int64(rowid) > l.max {
			l.max = int64(rowid)
		}
		if root > 0 {
			l.roots = append(l.roots, root)
		}
	}
	return l, true
}

// localPayload is how much of a table leaf's row of size bytes is on its page.
func localPayload(size uint64, usable int) int {
	x := uint64(usable - 35)
	if size <= x {
		return int(size)
	}
	m := uint64((usable-12)*32/255 - 23)
	k := m + (size-m)%uint64(usable-4)
	if k <= x {
		return int(k)
	}
	return int(m)
}

// schemaRow reads a record as a row of sqlite_master and returns its root page, 0 for none.
func schemaRow(rec []byte) (root int64, ok bool) {
	hdr, k := varint(rec)
	if k == 0 || hdr > uint64(len(rec)) {
		return 0, false
	}
	var types []uint64
	for at := k; at < int(hdr); {
		t, n := varint(rec[at:hdr])
		if n == 0 {
			return 0, false
		}
		types = append(types, t)
		at += n
	}
	if len(types) != 5 {
		return 0, false
	}
	text := func(t uint64) bool { return t >= 13 && t%2 == 1 }
	if !text(types[0]) || !text(types[1]) || !text(types[2]) || !(types[4] == 0 || text(types[4])) {
		return 0, false
	}
	at := int(hdr)
	kind := int((types[0] - 13) / 2)
	if at+kind > len(rec) {
		return 0, false
	}
	switch string(rec[at : at+kind]) {
	case "table", "index", "view", "trigger":
	default:
		return 0, false
	}
	// The root page follows the type, name and table name.
	at += kind + int((types[1]-13)/2) + int((types[2]-13)/2)
	switch t := types[3]; {
	case t == 0:
		return 0, true
	case t == 8:
		return 0, true
	case t == 9:
		return 1, true
	case t >= 1 && t <= 6:
		w := []int{0, 1, 2, 3, 4, 6, 8}[t]
		if at+w > len(rec) {
			// The row continues on an overflow page; its root page is unknown but the row is
			// still sqlite_master's.
			return 0, true
		}
		var v int64
		for _, c := range rec[at : at+w] {
			v = v<<8 | int64(c)
		}
		return v, true
	default:
		return 0, false
	}
}

// varint reads a SQLite varint from b and returns it with its length, 0 when b is too short.
func varint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < 9 && i < len(b); i++ {
		if i == 8 {
			return v<<8 | uint64(b[i]), 9
		}
		v = v<<7 | uint64(b[i]&0x7f)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}

// putVarint appends v as a SQLite varint.
func putVarint(out []byte, v uint64) []byte {
	if v > 0x00ffffffffffffff {
		var buf [9]byte
		buf[8] = byte(v)
		v >>= 8
		for i := 7; i >= 0; i-- {
			buf[i] = byte(v&0x7f) | 0x80
			v >>= 7
		}
		return append(out, buf[:]...)
	}
	var buf [9]byte
	n := 0
	for {
		buf[n] = byte(v&0x7f) | 0x80
		n++
		v >>= 7
		if v == 0 {
			break
		}
	}
	buf[0] &= 0x7f
	slices.Reverse(buf[:n])
	return append(out, buf[:n]...)
}

// writeFirstPage writes a header and a first page whose root points at leaves, in rowid order.
func writeFirstPage(path string, b []byte, leaves []schemaLeaf, ps, reserved int, wal bool) error {
	page := make([]byte, ps)
	copy(page, "SQLite format 3\x00")
	if ps == 65536 {
		binary.BigEndian.PutUint16(page[16:], 1)
	} else {
		binary.BigEndian.PutUint16(page[16:], uint16(ps))
	}
	page[18], page[19] = 1, 1 // read and written without a WAL
	if wal {
		page[18], page[19] = 2, 2
	}
	page[20] = byte(reserved)
	page[21], page[22], page[23] = 64, 32, 32
	binary.BigEndian.PutUint32(page[24:], 1)                 // change counter
	binary.BigEndian.PutUint32(page[28:], uint32(len(b)/ps)) // pages
	binary.BigEndian.PutUint32(page[40:], 1)                 // schema cookie
	binary.BigEndian.PutUint32(page[44:], 4)                 // schema format
	binary.BigEndian.PutUint32(page[56:], 1)                 // UTF-8
	binary.BigEndian.PutUint32(page[92:], 1)                 // valid for the change counter
	binary.BigEndian.PutUint32(page[96:], 3046000)

	// An interior table page: a cell per leaf but the last, keyed by its largest rowid, and
	// the last as the right child.
	usable := ps - reserved
	cells := leaves[:len(leaves)-1]
	end := usable
	page[100] = 0x05
	binary.BigEndian.PutUint16(page[103:], uint16(len(cells)))
	for i, l := range cells {
		cell := binary.BigEndian.AppendUint32(nil, uint32(l.page))
		cell = putVarint(cell, uint64(l.max))
		end -= len(cell)
		copy(page[end:], cell)
		binary.BigEndian.PutUint16(page[112+2*i:], uint16(end))
	}
	if end == 65536 {
		end = 0
	}
	binary.BigEndian.PutUint16(page[105:], uint16(end))
	binary.BigEndian.PutUint32(page[108:], uint32(leaves[len(leaves)-1].page))

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := f.WriteAt(page, 0); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
