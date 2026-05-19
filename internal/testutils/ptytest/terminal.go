package ptytest

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// ProcessRawOutput processes raw terminal output through a minimal terminal
// emulator, properly handling cursor movement and screen clearing sequences
// that bubbletea uses for in-place rendering.
//
// Unlike simple ANSI stripping (which loses cursor movement semantics and
// produces non-deterministic output depending on render batching), this
// function resolves cursor-up, clear-to-end, and other positioning sequences
// to produce the text that would actually be visible on a terminal screen.
//
// The resulting output is deterministic because regardless of how many
// intermediate renders bubbletea performed, the final screen state is the same.
func ProcessRawOutput(raw string) string {
	data := []byte(raw)

	// screen represents the terminal display as a slice of lines.
	var screen [][]rune
	row, col := 0, 0

	// ensureRow ensures the screen has at least r+1 rows.
	ensureRow := func(r int) {
		for len(screen) <= r {
			screen = append(screen, nil)
		}
	}

	// ensureCol ensures the given row has at least c+1 columns.
	ensureCol := func(r, c int) {
		ensureRow(r)
		for len(screen[r]) <= c {
			screen[r] = append(screen[r], ' ')
		}
	}

	i := 0
	for i < len(data) {
		ch := data[i]

		switch {
		case ch == '\x1b': // Escape sequence
			if i+1 >= len(data) {
				i++
				continue
			}

			if data[i+1] == '[' {
				// CSI sequence: \x1b[ <params> <command>
				i += 2

				// Parse parameter string (digits, semicolons, question mark).
				paramStart := i
				for i < len(data) && ((data[i] >= '0' && data[i] <= '9') || data[i] == ';' || data[i] == '?') {
					i++
				}
				params := string(data[paramStart:i])

				if i >= len(data) {
					continue
				}
				cmd := data[i]
				i++

				// Skip private CSI sequences (params starting with '?'),
				// e.g. \x1b[?25l (hide cursor), \x1b[?25h (show cursor).
				if strings.HasPrefix(params, "?") {
					continue
				}

				// parseParam returns the idx-th semicolon-separated numeric
				// parameter, or def if it's absent/zero.
				parseParam := func(idx int, def int) int {
					parts := strings.Split(params, ";")
					if idx >= len(parts) || parts[idx] == "" {
						return def
					}
					n, err := strconv.Atoi(parts[idx])
					if err != nil || n == 0 {
						return def
					}
					return n
				}

				switch cmd {
				case 'A': // Cursor up
					n := parseParam(0, 1)
					row -= n
					if row < 0 {
						row = 0
					}

				case 'B': // Cursor down
					n := parseParam(0, 1)
					row += n

				case 'C': // Cursor forward
					n := parseParam(0, 1)
					col += n

				case 'D': // Cursor back
					n := parseParam(0, 1)
					col -= n
					if col < 0 {
						col = 0
					}

				case 'E': // Cursor next line
					n := parseParam(0, 1)
					row += n
					col = 0

				case 'F': // Cursor previous line
					n := parseParam(0, 1)
					row -= n
					if row < 0 {
						row = 0
					}
					col = 0

				case 'G': // Cursor horizontal absolute
					col = parseParam(0, 1) - 1
					if col < 0 {
						col = 0
					}

				case 'H', 'f': // Cursor position (row;col, 1-based)
					row = parseParam(0, 1) - 1
					col = parseParam(1, 1) - 1
					if row < 0 {
						row = 0
					}
					if col < 0 {
						col = 0
					}

				case 'J': // Erase in display
					mode := parseParam(0, 0)
					switch mode {
					case 0: // From cursor to end of screen
						ensureRow(row)
						if col < len(screen[row]) {
							screen[row] = screen[row][:col]
						}
						// Remove all rows below the current one.
						screen = screen[:row+1]
					case 1: // From beginning to cursor
						for r := 0; r < row; r++ {
							if r < len(screen) {
								screen[r] = nil
							}
						}
						ensureRow(row)
						for c := 0; c <= col && c < len(screen[row]); c++ {
							screen[row][c] = ' '
						}
					case 2: // Entire screen
						screen = nil
						row, col = 0, 0
					}

				case 'K': // Erase in line
					mode := parseParam(0, 0)
					ensureRow(row)
					switch mode {
					case 0: // From cursor to end of line
						if col < len(screen[row]) {
							screen[row] = screen[row][:col]
						}
					case 1: // From beginning to cursor
						for c := 0; c <= col && c < len(screen[row]); c++ {
							screen[row][c] = ' '
						}
					case 2: // Entire line
						screen[row] = nil
					}

				case 'm': // SGR (colors/styles) — no visual output

				default: // Unknown CSI command — ignore
				}
			} else if data[i+1] == ']' {
				// OSC sequence: \x1b] ... (terminated by BEL or ST)
				i += 2
				for i < len(data) {
					if data[i] == '\x07' { // BEL terminator
						i++
						break
					}
					if data[i] == '\x1b' && i+1 < len(data) && data[i+1] == '\\' { // ST terminator
						i += 2
						break
					}
					i++
				}
			} else {
				// Other two-byte escape sequence — skip.
				i += 2
			}

		case ch == '\r':
			col = 0
			i++

		case ch == '\n':
			row++
			i++

		case ch == '\b':
			if col > 0 {
				col--
			}
			i++

		case ch == '\t':
			col = (col + 8) & ^7
			i++

		case ch >= 0x20 && ch < 0x7f: // Printable ASCII
			ensureCol(row, col)
			screen[row][col] = rune(ch)
			col++
			i++

		case ch >= 0x80: // Start of multi-byte UTF-8 character
			r, size := utf8.DecodeRune(data[i:])
			if r != utf8.RuneError {
				ensureCol(row, col)
				screen[row][col] = r
				col++
			}
			i += size

		default:
			// Other control characters (NUL, BEL, etc.) — skip.
			i++
		}
	}

	// Build output string, trimming trailing whitespace from each line.
	var lines []string
	for _, line := range screen {
		lines = append(lines, strings.TrimRight(string(line), " \t"))
	}

	return strings.Join(lines, "\n")
}
