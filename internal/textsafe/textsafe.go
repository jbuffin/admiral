// Package textsafe holds the rune rule for text that shows on a lock screen or in a prefix a reader must trust.
package textsafe

import "unicode"

// Hidden is true for a control, line or paragraph separator, bidi or zero-width rune: it can reorder text or fake a line break.
func Hidden(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || (r >= '​' && r <= '‍') || r == '\uFEFF' ||
		r == '\u2028' || r == '\u2029' || (r >= '‪' && r <= '‮') || (r >= '⁦' && r <= '⁩')
}
