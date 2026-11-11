package crew

// OriginSlug is the <owner>/<repo> of a github.com origin URL, as crew.py's origin_repo reads it.
func OriginSlug(origin string) (slug string, ok bool) { return "", false }

// NotesBlock is the heading block crew.py notes prints for a member's notes, empty when there are none.
func NotesBlock(n Notes, slug string) string { return "" }
