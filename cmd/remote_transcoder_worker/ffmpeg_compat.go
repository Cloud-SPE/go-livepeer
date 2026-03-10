//go:build cgo

package main

/*
 */
import "C"

// This file enables cgo so the weak symbol shims in ffmpeg_compat.c are linked
// into the worker binary.
