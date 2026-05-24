package sanitizer

import "strings"

// DeleteField removes the field at path from obj.
//
// Supported path formats:
//   - "status"                                     top-level key
//   - "metadata.resourceVersion"                   dot-separated nested keys
//   - `metadata.annotations["key/with.dots"]`      bracket notation for special keys
func DeleteField(obj map[string]any, path string) {
	segments := parsePath(path)
	if len(segments) == 0 {
		return
	}
	deleteAt(obj, segments)
}

// DeleteFields removes all paths from obj.
func DeleteFields(obj map[string]any, paths []string) {
	for _, p := range paths {
		DeleteField(obj, p)
	}
}

// parsePath splits a field path into key segments.
// Dot-separated keys outside brackets are individual segments.
// Bracket notation ["key"] extracts the key as a segment (quotes/escapes stripped).
func parsePath(path string) []string {
	var segments []string
	var cur strings.Builder
	inBracket := false
	inQuote := false

	i := 0
	for i < len(path) {
		c := path[i]
		switch {
		case c == '"' && inBracket:
			inQuote = !inQuote
			i++
		case c == '\\' && inBracket && inQuote && i+1 < len(path):
			i++ // skip backslash
			cur.WriteByte(path[i])
			i++
		case c == '[' && !inQuote:
			if cur.Len() > 0 {
				segments = append(segments, cur.String())
				cur.Reset()
			}
			inBracket = true
			i++
		case c == ']' && inBracket && !inQuote:
			if cur.Len() > 0 {
				segments = append(segments, cur.String())
				cur.Reset()
			}
			inBracket = false
			i++
		case c == '.' && !inBracket && !inQuote:
			if cur.Len() > 0 {
				segments = append(segments, cur.String())
				cur.Reset()
			}
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	if cur.Len() > 0 {
		segments = append(segments, cur.String())
	}
	return segments
}

func deleteAt(m map[string]any, segments []string) {
	if len(segments) == 1 {
		delete(m, segments[0])
		return
	}
	val, ok := m[segments[0]]
	if !ok {
		return
	}
	next, ok := val.(map[string]any)
	if !ok {
		return
	}
	deleteAt(next, segments[1:])
}
