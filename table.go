package htmlpdf

import "math"

type grid struct {
	header, body []*box
	ncols        int
	collapse     bool
	spacing      float64
}

func newGrid(t *box) *grid {
	g := &grid{collapse: t.st.collapse}
	if !g.collapse {
		g.spacing = t.st.spacing
	}
	var footer []*box
	for _, rg := range t.children {
		switch {
		case rg.header:
			g.header = append(g.header, rg.children...)
		case rg.footer:
			footer = append(footer, rg.children...)
		default:
			g.body = append(g.body, rg.children...)
		}
	}
	g.body = append(g.body, footer...)
	for _, r := range append(g.header, g.body...) {
		n := 0
		for _, c := range r.children {
			n += c.span
		}
		g.ncols = max(g.ncols, n)
	}
	return g
}

// edges returns the horizontal space the table uses outside its columns.
func (g *grid) edges(t *box) float64 {
	if g.collapse {
		return 0
	}
	pad, bw := padding(t.st, 0), borders(t.st)
	return pad[left] + pad[right] + bw[left] + bw[right] + g.spacing*float64(g.ncols+1)
}

// columns returns the min-content and max-content width of every column and
// any percentage width set on its cells.
func (l *layout) columns(g *grid) (mins, maxs, pcts []float64) {
	mins = make([]float64, g.ncols)
	maxs = make([]float64, g.ncols)
	pcts = make([]float64, g.ncols)
	type spanned struct {
		col, span int
		mn, mx    float64
	}
	var spans []spanned
	for _, r := range append(g.header, g.body...) {
		col := 0
		for _, c := range r.children {
			span := min(c.span, g.ncols-col)
			if span <= 0 {
				break
			}
			mn, mx := l.intrinsic(c)
			if span == 1 {
				mins[col] = math.Max(mins[col], mn)
				maxs[col] = math.Max(maxs[col], mx)
				if c.st.width.unit == pct {
					pcts[col] = math.Max(pcts[col], c.st.width.v)
				}
			} else {
				spans = append(spans, spanned{col, span, mn, mx})
			}
			col += span
		}
	}
	for _, s := range spans {
		gaps := g.spacing * float64(s.span-1)
		spread(mins[s.col:s.col+s.span], s.mn-gaps)
		spread(maxs[s.col:s.col+s.span], s.mx-gaps)
	}
	for i := range maxs {
		maxs[i] = math.Max(maxs[i], mins[i])
	}
	return mins, maxs, pcts
}

// spread grows cols evenly until they add up to at least total.
func spread(cols []float64, total float64) {
	var sum float64
	for _, c := range cols {
		sum += c
	}
	if extra := total - sum; extra > 0 {
		for i := range cols {
			cols[i] += extra / float64(len(cols))
		}
	}
}

func (l *layout) tableIntrinsic(t *box) (float64, float64) {
	g := newGrid(t)
	mins, maxs, _ := l.columns(g)
	var mn, mx float64
	for i := range mins {
		mn += mins[i]
		mx += maxs[i]
	}
	e := g.edges(t)
	if t.st.width.unit == pt {
		w := math.Max(t.st.width.v, mn+e)
		return w, w
	}
	return mn + e, mx + e
}

// distribute picks column widths that fill avail when fixed is set, and
// otherwise use at most avail.
func distribute(mins, maxs, pcts []float64, avail float64, fixed bool) []float64 {
	w := make([]float64, len(mins))
	free := avail
	var sumMin, sumMax, sumPct float64
	for i := range w {
		if pcts[i] > 0 {
			w[i] = math.Max(mins[i], pcts[i]*avail/100)
			free -= w[i]
			sumPct += w[i]
			fixed = true
			continue
		}
		sumMin += mins[i]
		sumMax += maxs[i]
	}
	switch {
	case free >= sumMax:
		extra := free - sumMax
		others := 0
		for i := range w {
			if pcts[i] == 0 {
				w[i] = maxs[i]
				others++
			}
		}
		if !fixed || extra <= 0 {
			break
		}
		for i := range w {
			switch {
			case others == 0:
				w[i] += extra * w[i] / sumPct
			case pcts[i] > 0:
			case sumMax > 0:
				w[i] += extra * maxs[i] / sumMax
			default:
				w[i] += extra / float64(others)
			}
		}
	case free <= sumMin:
		for i := range w {
			if pcts[i] == 0 {
				w[i] = mins[i]
			}
		}
	default:
		for i := range w {
			if pcts[i] == 0 {
				w[i] = mins[i] + (maxs[i]-mins[i])*(free-sumMin)/(sumMax-sumMin)
			}
		}
	}
	return w
}

func (l *layout) table(t *box, x, y, cb float64) float64 {
	st := t.st
	g := newGrid(t)
	mins, maxs, pcts := l.columns(g)
	e := g.edges(t)

	ml, mr := st.margin[left].resolve(cb), st.margin[right].resolve(cb)
	avail, fixed := cb-ml-mr, false
	if st.width.unit != auto {
		avail, fixed = st.width.resolve(cb), true
	}
	widths := distribute(mins, maxs, pcts, avail-e, fixed)
	tw := e
	for _, w := range widths {
		tw += w
	}
	switch {
	case st.margin[left].unit == auto && st.margin[right].unit == auto:
		ml = (cb - tw) / 2
	case st.margin[left].unit == auto:
		ml = cb - tw - mr
	}
	tx := x + ml

	cur := y
	if len(t.caption) > 0 {
		cur += l.blocks(t.caption, tx, cur, tw)
	}
	idx := l.push(item{kind: boxItem, x: tx, y: cur, w: tw, bg: st.bg, border: st.border, radius: st.radius, centered: g.collapse})

	var inset [4]float64
	if !g.collapse {
		pad, bw := padding(st, cb), borders(st)
		for i := range inset {
			inset[i] = pad[i] + bw[i]
		}
	}
	colX := make([]float64, g.ncols)
	gx := tx + inset[left] + g.spacing
	for i, w := range widths {
		colX[i] = gx
		gx += w + g.spacing
	}

	gy := cur + inset[top] + g.spacing
	headY, headN := gy, len(l.items)
	header := func() {
		for _, r := range g.header {
			gy += l.row(r, g, colX, widths, gy) + g.spacing
		}
	}
	header()
	headerH := gy - headY

	for i, r := range g.body {
		n, m := len(l.items), l.marker
		h := l.row(r, g, colX, widths, gy)
		if l.crosses(gy, h) && h+headerH <= l.pageH {
			if i == 0 {
				l.items, gy = l.items[:headN], l.nextPage(headY)
			} else {
				l.items, gy = l.items[:n], l.nextPage(gy)
			}
			l.marker = m
			header()
			h = l.row(r, g, colX, widths, gy)
		}
		gy += h + g.spacing
	}
	gy += inset[bottom]
	l.items[idx].h = gy - cur
	return gy - y
}

func (l *layout) row(r *box, g *grid, colX, widths []float64, y float64) float64 {
	if g.ncols == 0 {
		return 0
	}
	last := g.ncols - 1
	rowIdx := l.push(item{kind: boxItem, x: colX[0], y: y, w: colX[last] + widths[last] - colX[0], bg: r.st.bg})

	type placed struct {
		cell     *box
		idx, end int
		h        float64
	}
	var cells []placed
	var rowH float64
	if r.st.height.unit == pt {
		rowH = r.st.height.v
	}
	col := 0
	for _, c := range r.children {
		span := min(c.span, g.ncols-col)
		if span <= 0 {
			break
		}
		end := col + span - 1
		h, idx := l.boxAt(c, colX[col], y, colX[end]+widths[end]-colX[col], g.collapse)
		cells = append(cells, placed{c, idx, len(l.items), h})
		rowH = math.Max(rowH, h)
		col += span
	}
	for _, p := range cells {
		l.items[p.idx].h = rowH
		var dy float64
		switch p.cell.st.valign {
		case "middle":
			dy = (rowH - p.h) / 2
		case "bottom":
			dy = rowH - p.h
		}
		if dy > 0 {
			l.translate(p.idx+1, p.end, 0, dy)
		}
	}
	l.items[rowIdx].h = rowH
	return rowH
}
