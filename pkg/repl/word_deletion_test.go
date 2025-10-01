package repl

import (
	"testing"
)

func TestDeletePrevWord(t *testing.T) {
	// Create the deletePrevWord function (copy from repl.go for testing)
	deletePrevWord := func(line []rune, pos int) ([]rune, int) {
		if pos == 0 {
			return line, pos
		}
		// Skip any separators immediately before the cursor
		i := pos
		for i > 0 {
			c := byte(line[i-1])
			if isWordBoundary(c) {
				i--
				continue
			}
			break
		}
		// If we only found separators and reached start, delete just the separators
		if i == 0 {
			newLine := append([]rune(nil), line[pos:]...)
			return newLine, 0
		}
		// Then delete the previous word
		for i > 0 {
			c := byte(line[i-1])
			if isWordBoundary(c) {
				break
			}
			i--
		}
		newLine := append([]rune(nil), line[:i]...)
		newLine = append(newLine, line[pos:]...)
		return newLine, i
	}

	tests := []struct {
		name     string
		line     string
		pos      int
		wantLine string
		wantPos  int
	}{
		{
			name:     "delete word with separator after cursor (OK case from bug report)",
			line:     "sum by (foo)(foobar)",
			pos:      8, // cursor after "sum by (" at position of 'f'
			wantLine: "sum foo)(foobar)",
			wantPos:  4, // should delete "by (", leaving "sum "
		},
		{
			name:     "delete word when cursor is at beginning of word after separator (BUG case)",
			line:     "sum foo)(foobar)",
			pos:      4, // cursor after "sum "
			wantLine: "foo)(foobar)",
			wantPos:  0, // should delete "sum " not entire line
		},
		{
			name:     "delete word in middle",
			line:     "sum by job",
			pos:      10, // cursor at end
			wantLine: "sum by ",
			wantPos:  7,
		},
		{
			name:     "delete word with parenthesis",
			line:     "sum(rate(foo))",
			pos:      9, // cursor after "sum(rate("
			wantLine: "sum(foo))",
			wantPos:  4,
		},
		{
			name:     "cursor at beginning",
			line:     "sum by job",
			pos:      0,
			wantLine: "sum by job",
			wantPos:  0,
		},
		{
			name:     "only separators before cursor",
			line:     "  foo",
			pos:      2, // cursor after "  "
			wantLine: "foo",
			wantPos:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLine, gotPos := deletePrevWord([]rune(tt.line), tt.pos)
			gotLineStr := string(gotLine)
			if gotLineStr != tt.wantLine {
				t.Errorf("deletePrevWord() line = %q, want %q", gotLineStr, tt.wantLine)
			}
			if gotPos != tt.wantPos {
				t.Errorf("deletePrevWord() pos = %d, want %d", gotPos, tt.wantPos)
			}
		})
	}
}
