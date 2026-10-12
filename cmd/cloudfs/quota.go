package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/milin2436/cloudfs-139/sdk/cloudfs"
)

// quotaTable renders 139's account quota the way 139 thinks about it: in MB,
// with a second column for what that is in GiB, and the per-drive-type rows
// on top. Drive type is the finest scope 139 reports — there is no
// per-directory quota, so this cannot be keyed by a path.
//
// Items are not filtered: quotaList is the account's real inventory, and
// dropping the zero rows would make the table's shape change under the user.
// Everything is in --json too, but the table is what gets read.
func quotaTable(q *cloudfs.Quota, mount string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "mount:  %s\n\n", mount)

	used := q.DiskSizeMB - q.FreeDiskSizeMB
	rows := [][]string{
		{"总配额 diskSize", thousands(q.DiskSizeMB), giB(q.DiskSizeMB)},
	}
	for _, it := range q.Items {
		rows = append(rows, []string{it.Name + "usedSize", thousands(it.UsedMB), giB(it.UsedMB)})
	}
	rows = append(rows,
		[]string{"合计已用", thousands(used), giB(used)},
		[]string{"freeDiskSize", thousands(q.FreeDiskSizeMB), giB(q.FreeDiskSizeMB)},
	)

	b.WriteString(drawTable([]string{"", "MB", "≈"}, rows,
		[]align{alignLeft, alignRight, alignRight}))
	fmt.Fprintln(&b, "单位 MB（1 GiB = 1024 MB）；合计已用 = 总配额 − freeDiskSize，")
	fmt.Fprintln(&b, "freeDiskSize 为负即已超额。139 没有按目录的配额。")
	return b.String()
}

// align selects how a cell fills its column.
type align int

const (
	alignLeft align = iota
	alignRight
	alignCenter
)

// drawTable draws a box-drawing table, padding to display width so that CJK
// labels (two columns each) do not knock the border lines out of line.
func drawTable(header []string, rows [][]string, al []align) string {
	cols := len(header)
	width := make([]int, cols)
	for i, h := range header {
		width[i] = displayWidth(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < cols && displayWidth(c) > width[i] {
				width[i] = displayWidth(c)
			}
		}
	}

	var b strings.Builder
	// rule draws one border line. Corners go on the outside, the joiner
	// between columns, and w+2 is a cell's width including its one space of
	// padding on each side.
	rule := func(start, mid, end string) {
		b.WriteString(start)
		for i, w := range width {
			b.WriteString(strings.Repeat("─", w+2))
			if i < cols-1 {
				b.WriteString(mid)
			}
		}
		b.WriteString(end)
		b.WriteByte('\n')
	}
	cell := func(vals []string, a []align) {
		b.WriteString("│")
		for i, v := range vals {
			b.WriteString(" " + pad(v, width[i], a[i]) + " ")
			b.WriteString("│")
		}
		b.WriteByte('\n')
	}

	rule("┌", "┬", "┐")
	cell(header, []align{alignLeft, alignCenter, alignCenter})
	for _, r := range rows {
		rule("├", "┼", "┤")
		cell(r, al)
	}
	rule("└", "┴", "┘")
	return b.String()
}

// pad fits s into n display columns.
func pad(s string, n int, a align) string {
	gap := n - displayWidth(s)
	if gap < 0 {
		gap = 0
	}
	var left, right int
	switch a {
	case alignRight:
		left = gap
	case alignCenter:
		left, right = gap/2, gap/2
	default:
		right = gap
	}
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

// displayWidth counts terminal columns, treating CJK as two.
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		if isWide(r) {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// isWide approximates the East Asian Width property. It covers the ranges that
// actually occur in 139's drive names; box drawing and U+2212 stay single-wide.
func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E,   // CJK radicals, Kangxi, CJK symbols
		r >= 0x3041 && r <= 0x33FF,   // kana, kana supplemental, CJK compat
		r >= 0x3400 && r <= 0x4DBF,   // CJK unified ext A
		r >= 0x4E00 && r <= 0x9FFF,   // CJK unified
		r >= 0xA000 && r <= 0xA4CF,   // Yi
		r >= 0xAC00 && r <= 0xD7A3,   // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF,   // CJK compat ideographs
		r >= 0xFE30 && r <= 0xFE6F,   // CJK compat forms
		r >= 0xFF00 && r <= 0xFF60,   // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,   // fullwidth signs
		r >= 0x20000 && r <= 0x3FFFD: // CJK ext B and beyond
		return true
	}
	return false
}

// thousands adds comma separators and uses U+2212 for the sign so a negative
// amount reads as negative instead of looking like a dash.
func thousands(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "−" + string(out)
	}
	return string(out)
}

// giB renders an MB figure as a GiB label. Whole values drop the decimal so
// the quota reads "20 GiB" rather than "20.0 GiB".
func giB(mb int64) string {
	neg := mb < 0
	if neg {
		mb = -mb
	}
	v := float64(mb) / 1024
	s := fmt.Sprintf("%.1f GiB", v)
	if v == math.Trunc(v) {
		s = fmt.Sprintf("%.0f GiB", v)
	}
	if neg {
		return "−" + s
	}
	return s
}
