package main

import (
	"flag"
	"os"

	"github.com/golang/glog"
)

type Config struct {
	OrchAddr          string
	OrchSecret        string
	Capacity          int
	Backend           string
	NvidiaDevices     string
	QsvDevices        string
	WorkDir           string
	FfmpegPath        string
	TranscoderPreset  string
	TranscoderRC      string
	TranscoderCRF     int
	TranscoderMaxRate string
	TranscoderBufSize string
	TranscoderGOP     string
	TranscoderTune    string
	FfmpegLogLevel    string
}

func main() {
	cfg := parseConfig()
	if cfg.OrchAddr == "" {
		glog.Exit("missing -orchAddr")
	}
	if cfg.OrchSecret == "" {
		glog.Exit("missing -orchSecret")
	}
	if cfg.Capacity <= 0 {
		glog.Exit("capacity must be > 0")
	}
	if err := runWithBackoff(cfg); err != nil {
		glog.Exitf("remote transcoder worker exited: %v", err)
	}
}

func parseConfig() Config {
	var cfg Config

	flag.StringVar(&cfg.OrchAddr, "orchAddr", "", "Orchestrator address in host:port form")
	flag.StringVar(&cfg.OrchSecret, "orchSecret", "", "Shared secret with the orchestrator")
	flag.IntVar(&cfg.Capacity, "capacity", 1, "Transcoder capacity to register")
	flag.StringVar(&cfg.Backend, "transcoderBackend", "", "Override backend: nvidia or qsv")
	flag.StringVar(&cfg.NvidiaDevices, "nvidia", "", "Comma-separated NVIDIA GPU device IDs (or \"all\")")
	flag.StringVar(&cfg.QsvDevices, "qsv", "", "Comma-separated Intel QSV device IDs (or \"all\")")
	flag.StringVar(&cfg.WorkDir, "workDir", "/tmp/livepeer-workdir", "Working directory for segment staging")
	flag.StringVar(&cfg.FfmpegPath, "ffmpegPath", "ffmpeg", "Path to ffmpeg binary")
	flag.StringVar(&cfg.FfmpegLogLevel, "ffmpegLogLevel", "error", "ffmpeg loglevel (e.g. quiet, error, warning, info, debug)")

	flag.StringVar(&cfg.TranscoderPreset, "transcoderPreset", "", "Override transcoder preset")
	flag.StringVar(&cfg.TranscoderRC, "transcoderRC", "", "Override rate control")
	flag.IntVar(&cfg.TranscoderCRF, "transcoderCRF", 0, "Override CRF (0 means unset)")
	flag.StringVar(&cfg.TranscoderMaxRate, "transcoderMaxRate", "", "Override maxrate")
	flag.StringVar(&cfg.TranscoderBufSize, "transcoderBufSize", "", "Override bufsize")
	flag.StringVar(&cfg.TranscoderGOP, "transcoderGOP", "", "Override GOP")
	flag.StringVar(&cfg.TranscoderTune, "transcoderTune", "", "Override tune")

	flag.Parse()
	setFlags := flagSetMap()
	applyEnvOverrides(&cfg, setFlags)

	return cfg
}

func flagSetMap() map[string]bool {
	set := make(map[string]bool)
	flag.CommandLine.Visit(func(f *flag.Flag) {
		set[f.Name] = true
	})
	return set
}

func applyEnvOverrides(cfg *Config, set map[string]bool) {
	overrideString(&cfg.OrchAddr, "ORCH_ADDR", "orchAddr", set)
	overrideString(&cfg.OrchSecret, "ORCH_SECRET", "orchSecret", set)
	overrideInt(&cfg.Capacity, "TRANSCODER_CAPACITY", "capacity", set)
	overrideString(&cfg.Backend, "TRANSCODER_BACKEND", "transcoderBackend", set)
	overrideString(&cfg.NvidiaDevices, "NVIDIA_DEVICES", "nvidia", set)
	overrideString(&cfg.QsvDevices, "QSV_DEVICES", "qsv", set)
	overrideString(&cfg.WorkDir, "WORK_DIR", "workDir", set)
	overrideString(&cfg.FfmpegPath, "FFMPEG_PATH", "ffmpegPath", set)
	overrideString(&cfg.FfmpegLogLevel, "FFMPEG_LOGLEVEL", "ffmpegLogLevel", set)

	overrideString(&cfg.TranscoderPreset, "TRANSCODER_PRESET", "transcoderPreset", set)
	overrideString(&cfg.TranscoderRC, "TRANSCODER_RC", "transcoderRC", set)
	overrideInt(&cfg.TranscoderCRF, "TRANSCODER_CRF", "transcoderCRF", set)
	overrideString(&cfg.TranscoderMaxRate, "TRANSCODER_MAXRATE", "transcoderMaxRate", set)
	overrideString(&cfg.TranscoderBufSize, "TRANSCODER_BUFSIZE", "transcoderBufSize", set)
	overrideString(&cfg.TranscoderGOP, "TRANSCODER_GOP", "transcoderGOP", set)
	overrideString(&cfg.TranscoderTune, "TRANSCODER_TUNE", "transcoderTune", set)
}

func overrideString(dst *string, envKey, flagName string, set map[string]bool) {
	if set[flagName] {
		return
	}
	if v, ok := os.LookupEnv(envKey); ok {
		*dst = v
	}
}

func overrideInt(dst *int, envKey, flagName string, set map[string]bool) {
	if set[flagName] {
		return
	}
	if v, ok := os.LookupEnv(envKey); ok {
		parsed, err := parseEnvInt(v)
		if err == nil {
			*dst = parsed
		}
	}
}
