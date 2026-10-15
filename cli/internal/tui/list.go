package tui

// move moves sel through a list of n rows for an arrow or vim key, and reports whether k was one.
func move(sel *int, n int, k string) bool {
	switch k {
	case "j", "down":
		if *sel < n-1 {
			*sel++
		}
	case "k", "up":
		if *sel > 0 {
			*sel--
		}
	default:
		return false
	}
	return true
}

// listLines draws rows in a window of n lines of width w that keeps sel in view; off is where the window starts.
func listLines(rows []string, sel int, off *int, n, w int, active bool) []string {
	if n <= 0 {
		return nil
	}
	if sel < *off {
		*off = sel
	}
	if sel >= *off+n {
		*off = sel - n + 1
	}
	*off = max(0, min(*off, len(rows)-n))
	var out []string
	for i := *off; i < len(rows) && len(out) < n; i++ {
		if i == sel {
			out = append(out, selectedRow(rows[i], w, active))
		} else {
			out = append(out, plainRow(rows[i], w))
		}
	}
	for len(out) < n {
		out = append(out, "")
	}
	return out
}

// columns lays cells out at the given widths, two spaces apart, each cell cut to its width; the last takes the rest.
func columns(cells []string, widths []int) string {
	var s string
	for i, c := range cells {
		if i < len(widths) {
			s += fit(c, widths[i]) + "  "
		} else {
			s += c
		}
	}
	return s
}
