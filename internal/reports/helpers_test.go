package reports

// cellsOf returns the cells a row has, by key, among cols: the row as a map,
// for a count or a failure message.
func cellsOf(r Row, cols []Column) map[string]string {
	out := map[string]string{}
	for _, c := range cols {
		if v, ok := r.Get(c.Key); ok {
			out[c.Key] = v
		}
	}
	return out
}

// tableOf builds a table by hand: each row has exactly the cells its map
// gives, whatever the columns, as a decoded or merged row may.
func tableOf(cols []Column, rows ...map[string]string) Table {
	t := Table{Columns: cols}
	for _, m := range rows {
		l := &layout{pos: map[string]int{}, header: len(m)}
		var cells []string
		for k, v := range m {
			l.pos[k] = len(cells)
			cells = append(cells, v)
		}
		t.Rows = append(t.Rows, Row{l: l, cells: cells})
	}
	return t
}
