package main

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/livepeer/go-livepeer/core"
)

const protoVerLPT = "Livepeer-Transcoder-1.0"
const transcodingErrorMimeType = "livepeer/transcoding-error"

var errSecret = errors.New("invalid secret")
var errZeroCapacity = errors.New("zero capacity")
var errInterrupted = errors.New("execution interrupted")
var errCapabilities = errors.New("incompatible segment capabilities")
var errNotImplemented = errors.New("external ffmpeg execution not implemented")
var errFormat = errors.New("unrecognized profile output format")
var errProfile = errors.New("unrecognized encoder profile")
var errEncoder = errors.New("unrecognized video codec")
var errDuration = errors.New("invalid duration")

func checkTranscoderError(err error) error {
	if err == nil {
		return nil
	}
	s := status.Convert(err)
	if s.Message() == errSecret.Error() {
		return core.NewRemoteTranscoderFatalError(errSecret)
	}
	if s.Message() == errZeroCapacity.Error() {
		return core.NewRemoteTranscoderFatalError(errZeroCapacity)
	}
	if status.Code(err) == codes.Canceled {
		return core.NewRemoteTranscoderFatalError(errInterrupted)
	}
	return err
}

func isFatalTranscoderError(err error) bool {
	if err == nil {
		return false
	}
	_, fatal := err.(core.RemoteTranscoderFatalError)
	return fatal
}
