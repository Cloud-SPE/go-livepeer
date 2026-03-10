package main

import (
	"fmt"
	"time"

	"github.com/golang/glog"

	"github.com/livepeer/go-livepeer/common"
	"github.com/livepeer/go-livepeer/core"
	"github.com/livepeer/go-livepeer/net"
	"github.com/livepeer/lpms/ffmpeg"

	ethcommon "github.com/ethereum/go-ethereum/common"
)

func segDataToMetadata(segData *net.SegData) (*core.SegTranscodingMetadata, error) {
	if segData == nil {
		glog.Error("Empty seg data")
		return nil, fmt.Errorf("empty seg data")
	}
	var err error
	profiles := []ffmpeg.VideoProfile{}
	if len(segData.FullProfiles3) > 0 {
		profiles, err = makeFfmpegVideoProfiles(segData.FullProfiles3)
	} else if len(segData.FullProfiles2) > 0 {
		profiles, err = makeFfmpegVideoProfiles(segData.FullProfiles2)
	} else if len(segData.FullProfiles) > 0 {
		profiles, err = makeFfmpegVideoProfiles(segData.FullProfiles)
	}
	if err != nil {
		glog.Error("Unable to deserialize profiles ", err)
		return nil, err
	}

	var os *net.OSInfo
	if len(segData.Storage) > 0 {
		os = segData.Storage[0]
	}

	dur := time.Duration(segData.Duration) * time.Millisecond
	if dur < 0 || dur > common.MaxDuration {
		glog.Error("Invalid duration")
		return nil, errDuration
	}
	if dur == 0 {
		dur = 2 * time.Second
	}

	caps := core.CapabilitiesFromNetCapabilities(segData.Capabilities)
	if caps == nil {
		caps = core.NewCapabilities(nil, nil)
	}

	var segPar core.SegmentParameters
	segPar.ForceSessionReinit = segData.ForceSessionReinit
	if segData.SegmentParameters != nil {
		segPar.Clip = &core.SegmentClip{
			From: time.Duration(segData.SegmentParameters.From) * time.Millisecond,
			To:   time.Duration(segData.SegmentParameters.To) * time.Millisecond,
		}
	}

	return &core.SegTranscodingMetadata{
		ManifestID:         core.ManifestID(segData.ManifestId),
		Seq:                segData.Seq,
		Hash:               ethcommon.BytesToHash(segData.Hash),
		Profiles:           profiles,
		OS:                 os,
		Duration:           dur,
		Caps:               caps,
		AuthToken:          segData.AuthToken,
		CalcPerceptualHash: segData.CalcPerceptualHash,
		SegmentParameters:  &segPar,
	}, nil
}

func makeFfmpegVideoProfiles(protoProfiles []*net.VideoProfile) ([]ffmpeg.VideoProfile, error) {
	profiles := make([]ffmpeg.VideoProfile, 0, len(protoProfiles))
	for _, profile := range protoProfiles {
		name := profile.Name
		if name == "" {
			name = "net_" + ffmpeg.DefaultProfileName(int(profile.Width), int(profile.Height), int(profile.Bitrate))
		}
		format := ffmpeg.FormatMPEGTS
		switch profile.Format {
		case net.VideoProfile_MPEGTS:
		case net.VideoProfile_MP4:
			format = ffmpeg.FormatMP4
		default:
			return nil, errFormat
		}
		encoderProf := ffmpeg.ProfileNone
		switch profile.Profile {
		case net.VideoProfile_ENCODER_DEFAULT:
		case net.VideoProfile_H264_BASELINE:
			encoderProf = ffmpeg.ProfileH264Baseline
		case net.VideoProfile_H264_MAIN:
			encoderProf = ffmpeg.ProfileH264Main
		case net.VideoProfile_H264_HIGH:
			encoderProf = ffmpeg.ProfileH264High
		case net.VideoProfile_H264_CONSTRAINED_HIGH:
			encoderProf = ffmpeg.ProfileH264ConstrainedHigh
		default:
			return nil, errProfile
		}
		encoder := ffmpeg.H264
		switch profile.Encoder {
		case net.VideoProfile_H264:
			encoder = ffmpeg.H264
		case net.VideoProfile_H265:
			encoder = ffmpeg.H265
		case net.VideoProfile_VP8:
			encoder = ffmpeg.VP8
		case net.VideoProfile_VP9:
			encoder = ffmpeg.VP9
		default:
			return nil, errEncoder
		}
		var gop time.Duration
		if profile.Gop < 0 {
			gop = time.Duration(profile.Gop)
		} else {
			gop = time.Duration(profile.Gop) * time.Millisecond
		}
		prof := ffmpeg.VideoProfile{
			Name:         name,
			Bitrate:      fmt.Sprint(profile.Bitrate),
			Framerate:    uint(profile.Fps),
			FramerateDen: uint(profile.FpsDen),
			Resolution:   fmt.Sprintf("%dx%d", profile.Width, profile.Height),
			Format:       format,
			Profile:      encoderProf,
			GOP:          gop,
			Encoder:      encoder,
			Quality:      uint(profile.Quality),
		}
		profiles = append(profiles, prof)
	}
	return profiles, nil
}
