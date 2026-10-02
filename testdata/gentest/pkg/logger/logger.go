// Package logger is the demo fixture's stand-in for the converted host's
// logging package.
//
// It exists so the generated suites can actually be executed. The suites call
// logger.LoggerInit in SetupSuite and then write through the package-level
// Debugf the store methods call; without these symbols the fixture cannot
// compile, which is why the byte-pinned goldens could assert a passing-shaped
// file that no one ever ran.
//
// The real host implementation is irrelevant to what is under test — the
// generated assertions never inspect log output — so this discards its input
// rather than reproducing logrus formatting.
package logger

import "log"

// LoggerInit mirrors the host signature: a file path and a level, neither of
// which the generated suites rely on.
func LoggerInit(path string, level int) {
	log.SetFlags(0)
	log.SetOutput(discard{})
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// The Debugf family the converted store and controller bodies call. Each is a
// no-op for the same reason as LoggerInit.
func Debugf(format string, args ...interface{}) {}
func Infof(format string, args ...interface{})  {}
func Warnf(format string, args ...interface{})  {}
func Errorf(format string, args ...interface{}) {}
func Debug(args ...interface{})                 {}
func Info(args ...interface{})                  {}
func Warn(args ...interface{})                  {}
func Error(args ...interface{})                 {}
