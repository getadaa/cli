package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Table prints rows aligned for a person, or tab-separated without a header
// when stdout is not a terminal, so `cut -f1` and `awk` work on it.
type Table struct {
	io      *IO
	headers []string
	rows    [][]string
	// flex is the column that gives up width when the terminal is narrow.
	flex int
}

func (i *IO) NewTable(headers ...string) *Table {
	return &Table{io: i, headers: headers, flex: -1}
}

// Flex marks the column to truncate first, usually a free-text summary.
func (t *Table) Flex(col int) *Table {
	t.flex = col
	return t
}

func (t *Table) Row(cells ...string) {
	t.rows = append(t.rows, cells)
}

func (t *Table) Len() int { return len(t.rows) }

func (t *Table) Render() {
	w := t.io.Out
	if !t.io.OutTTY {
		for _, r := range t.rows {
			plain := make([]string, len(r))
			for j, c := range r {
				plain[j] = strings.ReplaceAll(ansi.Strip(c), "\t", " ")
			}
			fmt.Fprintln(w, strings.Join(plain, "\t"))
		}
		return
	}

	widths := make([]int, len(t.headers))
	for j, h := range t.headers {
		widths[j] = lipgloss.Width(h)
	}
	for _, r := range t.rows {
		for j := range min(len(r), len(widths)) {
			widths[j] = max(widths[j], lipgloss.Width(r[j]))
		}
	}

	const gap = 2
	if tw := t.io.Width(); tw > 0 && t.flex >= 0 && t.flex < len(widths) {
		total := 0
		for _, cw := range widths {
			total += cw + gap
		}
		if over := total - gap - tw; over > 0 {
			widths[t.flex] = max(widths[t.flex]-over, 12)
		}
	}

	pal := t.io.S()
	cells := make([]string, len(t.headers))
	for j, h := range t.headers {
		cells[j] = pad(pal.Dim(strings.ToUpper(h)), widths[j], j == len(t.headers)-1)
	}
	fmt.Fprintln(w, strings.Join(cells, strings.Repeat(" ", gap)))
	for _, r := range t.rows {
		for j := range t.headers {
			c := ""
			if j < len(r) {
				c = r[j]
			}
			if lipgloss.Width(c) > widths[j] {
				c = ansi.Truncate(c, widths[j], "…")
			}
			cells[j] = pad(c, widths[j], j == len(t.headers)-1)
		}
		fmt.Fprintln(w, strings.TrimRight(strings.Join(cells, strings.Repeat(" ", gap)), " "))
	}
}

func pad(s string, width int, last bool) string {
	if last {
		return s
	}
	if d := width - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// Detail prints one record: a title line, then aligned label/value pairs,
// grouped under optional section headings. Empty values are skipped, because
// a screen full of "—" hides the fields that matter.
type Detail struct {
	io       *IO
	title    string
	subtitle string
	blocks   []detailBlock
}

type detailBlock struct {
	heading string
	fields  [][2]string
	lines   []string
}

func (i *IO) NewDetail(title, subtitle string) *Detail {
	return &Detail{io: i, title: title, subtitle: subtitle, blocks: []detailBlock{{}}}
}

func (d *Detail) Field(label, value string) *Detail {
	if strings.TrimSpace(ansi.Strip(value)) == "" {
		return d
	}
	b := &d.blocks[len(d.blocks)-1]
	b.fields = append(b.fields, [2]string{label, value})
	return d
}

// Section starts a new group. Sections with nothing in them are not printed.
func (d *Detail) Section(heading string) *Detail {
	d.blocks = append(d.blocks, detailBlock{heading: heading})
	return d
}

// Line adds free text to the current section, such as a paragraph or a list item.
func (d *Detail) Line(text string) *Detail {
	b := &d.blocks[len(d.blocks)-1]
	b.lines = append(b.lines, text)
	return d
}

func (d *Detail) Render() {
	w := d.io.Out
	pal := d.io.S()
	if d.title != "" {
		title := pal.Bold(d.title)
		if d.subtitle != "" {
			title += "  " + pal.Dim(d.subtitle)
		}
		fmt.Fprintln(w, title)
	}

	labelWidth := 0
	for _, b := range d.blocks {
		for _, f := range b.fields {
			labelWidth = max(labelWidth, lipgloss.Width(f[0]))
		}
	}
	for _, b := range d.blocks {
		if len(b.fields) == 0 && len(b.lines) == 0 {
			continue
		}
		if b.heading != "" {
			fmt.Fprintln(w)
			fmt.Fprintln(w, pal.Bold(b.heading))
		}
		for _, f := range b.fields {
			value := strings.ReplaceAll(f[1], "\n", "\n  "+strings.Repeat(" ", labelWidth+2))
			fmt.Fprintf(w, "  %s  %s\n", pad(pal.Dim(f[0]), labelWidth, false), value)
		}
		for _, l := range b.lines {
			fmt.Fprintln(w, "  "+strings.ReplaceAll(l, "\n", "\n  "))
		}
	}
}
