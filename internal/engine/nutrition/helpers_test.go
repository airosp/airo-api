package nutrition

import (
	"time"
)

// A mesma âncora usada pelo gerador em node.
func time0ISO(dayOffset, hour int) string {
	t := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, dayOffset).Add(time.Duration(hour) * time.Hour)
	return t.Format("2006-01-02T15:04:05.000Z")
}
