package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func nowStamp() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func FmtNum(v float64) string {
	if v >= 1e9 {
		return fmt.Sprintf("%.2f B", v/1e9)
	}
	if v >= 1e6 {
		return fmt.Sprintf("%.2f M", v/1e6)
	}
	if v >= 1e4 {
		return fmt.Sprintf("%.1f k", v/1e3)
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%g", v)
}

func FmtBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return itoa(int(b)) + " B"
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	return fmt.Sprintf("%.1f %s", float64(b)/float64(div), units[exp])
}

func FmtDuration(secs float64) string {
	if secs <= 0 {
		return "-"
	}
	s := int64(secs)
	d := s / 86400
	h := (s % 86400) / 3600
	m := (s % 3600) / 60
	sec := s % 60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh %02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %02ds", m, sec)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}

func FmtTime(t time.Time) string {
	if t.IsZero() || t.Unix() == 0 {
		return "-"
	}
	return t.Local().Format("Jan 02 15:04")
}

func FmtAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return itoa(int(d.Minutes())) + " min ago"
	case d < 24*time.Hour:
		return itoa(int(d.Hours())) + " h ago"
	default:
		return itoa(int(d.Hours()/24)) + " d ago"
	}
}

var projColorCache = map[string]string{}

func ProjColor(key string) string {
	if c, ok := projColorCache[key]; ok {
		return c
	}
	var h uint32
	for _, r := range key {
		h = h*31 + uint32(r)
	}
	c := fmt.Sprintf("hsl(%d, 65%%, 55%%)", h%360)
	projColorCache[key] = c
	return c
}
