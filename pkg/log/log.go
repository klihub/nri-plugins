// Copyright 2019-2020 Intel Corporation. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package log

import (
	"fmt"
	stdlog "log"
	"log/slog"
	"os"
	"path/filepath"

	cfgapi "github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/log"
)

const (
	// DefaultLevel is the default logging severity level.
	DefaultLevel = LevelInfo
	// EnvVarDebug is the environment variable used to seed debugging flags.
	EnvVarDebug = "LOGGER_DEBUG"
	// EnvVarSource is the environment variable used to seed source logging.
	EnvVarSource = "LOGGER_LOG_SOURCE"
)

// Level describes the severity of a log message.
type Level int

const (
	// levelUnset denotes an unset level.
	levelUnset Level = iota
	// LevelDebug is the severity for debug messages.
	LevelDebug
	// LevelInfo is the severity for informational messages.
	LevelInfo
	// LevelWarn is the severity for warnings.
	LevelWarn
	// LevelError is the severity for errors.
	LevelError
	// LevelPanic is the severity for panic messages.
	LevelPanic
	// LevelFatal is the severity for fatal errors.
	LevelFatal
)

type (
	Logger interface {
		DebugEnabled() bool

		Debug(msg string, args ...any)
		Info(msg string, args ...any)
		Warn(msg string, args ...any)
		Error(msg string, args ...any)
		Fatal(msg string, args ...any)
		Panic(msg string, args ...any)

		Debugf(msg string, args ...any)
		Infof(msg string, args ...any)
		Warnf(msg string, args ...any)
		Errorf(msg string, args ...any)
		Fatalf(msg string, args ...any)
		Panicf(msg string, args ...any)

		DebugBlock(msg string, args ...any)
		InfoBlock(msg string, args ...any)
		WarnBlock(msg string, args ...any)
		ErrorBlock(msg string, args ...any)

		Flush()
		Write(p []byte) (n int, err error)
	}

	logger struct {
		*slog.Logger
	}
)

var (
	_       Logger = (*logger)(nil)
	deflog  Logger
	sLogger = slog.New(slog.NewTextHandler(os.Stderr, nil))
)

func SetStdLogger(source string) {
	stdlog.SetPrefix("")
	stdlog.SetFlags(0)
	stdlog.SetOutput(Get(source))
}

func Configure(cfg *cfgapi.Config) error {
	deflog.Info("logger configuration update %+v", cfg)
	return nil
}

func Get(source string) Logger {
	return &logger{
		Logger: sLogger.With("source", source),
	}
}

func NewLogger(source string) Logger {
	return Get(source)
}

func Default() Logger {
	return deflog
}

func Flush() {
	// a nop
}

func (l *logger) DebugEnabled() bool {
	return true
}

func (l *logger) Debug(format string, args ...any) {
	l.Logger.Debug(fmt.Sprintf(format, args...))
}

func (l *logger) Info(format string, args ...any) {
	l.Logger.Info(fmt.Sprintf(format, args...))
}

func (l *logger) Warn(format string, args ...any) {
	l.Logger.Warn(fmt.Sprintf(format, args...))
}

func (l *logger) Error(format string, args ...any) {
	l.Logger.Error(fmt.Sprintf(format, args...))
}

func (l *logger) Fatal(msg string, args ...any) {
	l.Logger.Error(fmt.Sprintf(msg, args...))
	os.Exit(1)
}

func (l *logger) Panic(msg string, args ...any) {
	l.Logger.Error(fmt.Sprintf(msg, args...))
	panic(fmt.Sprintf(msg, args...))
}

func (l *logger) Debugf(format string, args ...any) {
	l.Logger.Debug(fmt.Sprintf(format, args...))
}

func (l *logger) Infof(format string, args ...any) {
	l.Logger.Info(fmt.Sprintf(format, args...))
}

func (l *logger) Warnf(format string, args ...any) {
	l.Logger.Warn(fmt.Sprintf(format, args...))
}

func (l *logger) Errorf(format string, args ...any) {
	l.Logger.Error(fmt.Sprintf(format, args...))
}

func (l *logger) Fatalf(msg string, args ...any) {
	l.Logger.Error(fmt.Sprintf(msg, args...))
	os.Exit(1)
}

func (l *logger) Panicf(msg string, args ...any) {
	l.Logger.Error(fmt.Sprintf(msg, args...))
	panic(fmt.Sprintf(msg, args...))
}

func (l *logger) DebugBlock(format string, args ...any) {
	l.Logger.Debug(fmt.Sprintf(format, args...))
}

func (l *logger) InfoBlock(format string, args ...any) {
	l.Logger.Info(fmt.Sprintf(format, args...))
}

func (l *logger) WarnBlock(format string, args ...any) {
	l.Logger.Warn(fmt.Sprintf(format, args...))
}

func (l *logger) ErrorBlock(format string, args ...any) {
	l.Logger.Error(fmt.Sprintf(format, args...))
}

func (l *logger) Flush() {
	// a no-op
}

func (l *logger) Write(p []byte) (int, error) {
	Default().Debug("%s", string(p))
	return len(p), nil
}

func init() {
	binary := filepath.Clean(os.Args[0])
	source := filepath.Base(binary)
	deflog = Get(source)
}
