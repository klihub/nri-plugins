// Copyright The NRI Plugins Authors. All Rights Reserved.
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
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type slogger struct {
	source string
	*slog.Logger
	ctx context.Context
}

var _ Logger = &slogger{}

func Get(source string) Logger {
	return NewLogger(source)
}

func NewLogger(source string) Logger {
	if source == "" {
		source = filepath.Base(filepath.Clean(os.Args[0]))
	}

	cfg.addSource(source)

	return &slogger{
		source: source,
		Logger: slog.New(
			slog.NewMultiHandler(
				NewSlogHandler(source),
				NewOtelHandler(source),
			),
		),
		ctx: context.TODO(),
	}
}

func (l *slogger) Log(lvl Level, msg string, attrs ...Attr) {
	l.LogAttrs(l.ctx, lvl, msg, attrs...)
}

func (l *slogger) Debug(msg string, attrs ...Attr) {
	l.Log(LevelDebug, msg, attrs...)
}

func (l *slogger) Info(msg string, attrs ...Attr) {
	l.Log(LevelInfo, msg, attrs...)
}

func (l *slogger) Warn(msg string, attrs ...Attr) {
	l.Log(LevelWarn, msg, attrs...)
}

func (l *slogger) Error(msg string, attrs ...Attr) {
	l.Log(LevelError, msg, attrs...)
}

func (l *slogger) Fatal(msg string, attrs ...Attr) {
	l.Log(LevelFatal, msg, attrs...)
	os.Exit(1)
}

func (l *slogger) Panic(msg string, attrs ...Attr) {
	l.Log(LevelPanic, msg, attrs...)
	panic(msg)
}

func (l *slogger) Debugf(format string, args ...any) {
	l.Log(LevelDebug, fmt.Sprintf(format, args...))
}

func (l *slogger) Infof(format string, args ...any) {
	l.Log(LevelInfo, fmt.Sprintf(format, args...))
}

func (l *slogger) Warnf(format string, args ...any) {
	l.Log(LevelWarn, fmt.Sprintf(format, args...))
}

func (l *slogger) Errorf(format string, args ...any) {
	l.Log(LevelError, fmt.Sprintf(format, args...))
}

func (l *slogger) Fatalf(format string, args ...any) {
	l.Log(LevelFatal, fmt.Sprintf(format, args...))
	os.Exit(1)
}

func (l *slogger) Panicf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.Log(LevelPanic, msg)
	panic(msg)
}

func (l *slogger) LogContext(ctx context.Context, lvl Level, msg string, attrs ...Attr) {
	l.LogAttrs(ctx, lvl, msg, attrs...)
}

func (l *slogger) LogBlock(lvl Level, prefix, format string, args ...any) {
	for _, msg := range strings.Split(fmt.Sprintf(format, args...), "\n") {
		l.Log(lvl, prefix+" "+msg)
	}
}

func (l *slogger) DebugBlock(prefix, format string, args ...any) {
	l.LogBlock(LevelDebug, prefix, format, args...)
}

func (l *slogger) InfoBlock(prefix, format string, args ...any) {
	l.LogBlock(LevelInfo, prefix, format, args...)
}

func (l *slogger) WarnBlock(prefix, format string, args ...any) {
	l.LogBlock(LevelWarn, prefix, format, args...)
}

func (l *slogger) ErrorBlock(prefix, format string, args ...any) {
	l.LogBlock(LevelError, prefix, format, args...)
}

func (l *slogger) WithContext(ctx context.Context) Logger {
	return &slogger{
		ctx:    ctx,
		source: l.source,
		Logger: l.Logger,
	}
}

func (l *slogger) WithGroup(name string) Logger {
	return &slogger{
		ctx:    l.ctx,
		source: l.source,
		Logger: l.Logger.WithGroup(name),
	}
}

func (l *slogger) WithAttrs(args ...any) Logger {
	return &slogger{
		ctx:    l.ctx,
		source: l.source,
		Logger: l.With(args...),
	}
}

func (l *slogger) DebugEnabled() bool {
	return cfg.Debugging(l.source)
}

func (l *slogger) EnableDebug(enable bool) bool {
	return cfg.EnableDebug(l.source, enable)
}

func (l *slogger) Flush() {
}

func (l *slogger) SlogHandler() slog.Handler {
	return l.Handler()
}

type SlogHandler struct {
	handler slog.Handler
	source  string
}

func NewSlogHandler(source string) *SlogHandler {
	h := &SlogHandler{
		source: source,
	}
	h.handler = slog.NewTextHandler(h, &slog.HandlerOptions{ReplaceAttr: h.ReplaceAttr})
	return h
}

func (o *SlogHandler) WithGroup(name string) slog.Handler {
	return &SlogHandler{
		source:  o.source,
		handler: o.handler.WithGroup(name),
	}
}

func (o *SlogHandler) WithAttrs(attrs []Attr) slog.Handler {
	attrs = slices.Clone(attrs)
	return &SlogHandler{
		source:  o.source,
		handler: o.handler.WithAttrs(attrs),
	}
}

func (o *SlogHandler) Enabled(ctx context.Context, level slog.Level) (enabled bool) {
	if level > LevelDebug {
		return o.handler.Enabled(ctx, level)
	}
	return cfg.Debugging(o.source)
}

func (o *SlogHandler) Handle(ctx context.Context, r slog.Record) error {
	if cfg.SkipHeaders() && cfg.LogSource() {
		r.Time = time.Time{}
	}

	return o.handler.Handle(ctx, r)
}

func (o *SlogHandler) ReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && cfg.SkipHeaders() && cfg.LogSource() && a.Key == slog.LevelKey {
		return slog.Attr{Key: a.Key, Value: slog.StringValue(a.Value.String()[:1])}
	}
	return a
}

func (o *SlogHandler) formatLevelAndSource(entry []byte) (level, source, rest []byte) {
	if !(cfg.SkipHeaders() && cfg.LogSource()) {
		return nil, nil, entry
	}

	var (
		key       = []byte("level=")
		pre, post int
	)

	if !bytes.HasPrefix(entry, key) {
		return nil, nil, entry
	}

	level = entry[len(key) : len(key)+1]
	rest = entry[len(key)+1+1:]

	pre, post = cfg.PadSource(o.source)
	src := bytes.NewBuffer(make([]byte, 0, pre+post+len(o.source)))
	fmt.Fprintf(src, "[%*.*s%s%*.*s] ", pre, pre, "", o.source, post, post, "")

	return level, src.Bytes(), rest
}

func (o *SlogHandler) Write(p []byte) (n int, err error) {
	level, source, msg := o.formatLevelAndSource(p)
	if level != nil {
		fmt.Fprintf(out, "%s: ", string(level))
	}
	if source != nil {
		fmt.Fprintf(out, string(source))
	}
	return out.Write(msg)
}
