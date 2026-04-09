package proxy

import (
	"strconv"
	"strings"
)

// NormalizePath strips the query string and replaces variable path segments
// (hex identifiers and numeric IDs) with typed placeholders. Named segments
// like "head", "finalized", "genesis" are left as-is.
func NormalizePath(path string) string {
	if q := strings.IndexByte(path, '?'); q != -1 {
		path = path[:q]
	}

	parts := strings.Split(path, "/")

	for i, p := range parts {
		if i < 2 {
			continue
		}

		if strings.HasPrefix(p, "0x") {
			parts[i] = "{hex}"
			continue
		}

		if _, err := strconv.ParseUint(p, 10, 64); err == nil {
			parts[i] = "{id}"
		}
	}

	return strings.Join(parts, "/")
}
