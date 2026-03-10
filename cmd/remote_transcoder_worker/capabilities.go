package main

import (
	"fmt"
	"strings"
)

type Backend string

const (
	BackendNvidia   Backend = "nvidia"
	BackendQSV      Backend = "qsv"
	BackendSoftware Backend = "software"
)

func selectBackend(cfg Config) (Backend, error) {
	if cfg.Backend != "" {
		b := Backend(strings.ToLower(cfg.Backend))
		switch b {
		case BackendNvidia, BackendQSV, BackendSoftware:
			if b == BackendNvidia && cfg.NvidiaDevices == "" {
				return "", fmt.Errorf("backend nvidia selected but -nvidia not set")
			}
			if b == BackendQSV && cfg.QsvDevices == "" {
				return "", fmt.Errorf("backend qsv selected but -qsv not set")
			}
			return b, nil
		default:
			return "", fmt.Errorf("unknown backend: %s", cfg.Backend)
		}
	}

	// Auto selection order: nvidia -> qsv -> software
	if cfg.NvidiaDevices != "" {
		return BackendNvidia, nil
	}
	if cfg.QsvDevices != "" {
		return BackendQSV, nil
	}
	return BackendNvidia, nil
}
