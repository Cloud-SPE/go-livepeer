package main

import (
	"strconv"
	"strings"
	"time"
)

func parseEnvInt(raw string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(raw))
}

func timeNow() time.Time {
	return time.Now()
}

func since(t time.Time) time.Duration {
	return time.Since(t)
}
