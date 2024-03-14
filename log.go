// log.go
// Copyright(c) 2022 Matt Pharr, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package main

import (
	"fmt"
	"log/slog"
	"os"
	"path"
	"runtime"
	"runtime/debug"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

type Logger struct {
	*slog.Logger
	logFile string
	start   time.Time
}

func NewLogger(server bool, level string) *Logger {
	var w *lumberjack.Logger

	if server {
		w = &lumberjack.Logger{
			Filename: "vice-logs/slog",
			MaxSize:  64, // MB
			MaxAge:   14,
			Compress: true,
		}
	} else {
		dir, err := os.UserConfigDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to find user config dir: %v", err)
			dir = "."
		}
		fn := path.Join(dir, "Vice", "vice.slog")

		w = &lumberjack.Logger{
			Filename:   fn,
			MaxSize:    Select(level == "debug", 512, 32), // MB
			MaxBackups: 1,
		}
	}

	lvl := slog.LevelInfo
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		fmt.Fprintf(os.Stderr, "%s: invalid log level", level)
	}

	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl})
	l := &Logger{
		Logger:  slog.New(h),
		logFile: w.Filename,
		start:   time.Now(),
	}

	// Start out the logs with some basic information about the system
	// we're running on and the build of vice that's being used.
	l.Info("Hello logging", slog.Time("start", time.Now()))
	l.Info("System information",
		slog.String("GOARCH", runtime.GOARCH),
		slog.String("GOOS", runtime.GOOS),
		slog.Int("NumCPUs", runtime.NumCPU()))

	var deps, settings []any
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range bi.Deps {
			deps = append(deps, slog.String(dep.Path, dep.Version))
			if dep.Replace != nil {
				deps = append(deps, slog.String("Replacement "+dep.Replace.Path, dep.Replace.Version))
			}
		}
		for _, setting := range bi.Settings {
			settings = append(settings, slog.String(setting.Key, setting.Value))
		}

		l.Info("Build",
			slog.String("Go version", bi.GoVersion),
			slog.String("Path", bi.Path),
			slog.Group("Dependencies", deps...),
			slog.Group("Settings", settings...))
	}

	return l
}

// Debug wraps slog.Debug to add call stack information (and similarly for
// the following Logger methods...)  Note that we do not wrap the entire
// slog logging interface, so, for example, WarnContext and Log do not have
// callstacks included.
//
// We also wrap the logging methods to allow a nil *Logger, in which case
// debug and info messages are discarded (though warnings and errors still
// go through to slog.)
func (l *Logger) Debug(msg string, args ...any) {
	if l != nil && l.Logger.Enabled(nil, slog.LevelDebug) {
		args = append([]any{slog.Any("callstack", Callstack())}, args...)
		l.Logger.Debug(msg, args...)
	}
}

// Debugf is a convenience wrapper that logs just a message and allows
// printf-style formatting of the provided args.
func (l *Logger) Debugf(msg string, args ...any) {
	if l != nil && l.Logger.Enabled(nil, slog.LevelDebug) {
		l.Logger.Debug(fmt.Sprintf(msg, args...), slog.Any("callstack", Callstack()))
	}
}

func (l *Logger) Info(msg string, args ...any) {
	if l != nil && l.Logger.Enabled(nil, slog.LevelInfo) {
		args = append([]any{slog.Any("callstack", Callstack())}, args...)
		l.Logger.Info(msg, args...)
	}
}

func (l *Logger) Infof(msg string, args ...any) {
	if l != nil && l.Logger.Enabled(nil, slog.LevelInfo) {
		l.Logger.Info(fmt.Sprintf(msg, args...), slog.Any("callstack", Callstack()))
	}
}

func (l *Logger) Warn(msg string, args ...any) {
	args = append([]any{slog.Any("callstack", Callstack())}, args...)
	if l == nil {
		slog.Warn(msg, args...)
	} else {
		l.Logger.Warn(msg, args...)
	}
}

func (l *Logger) Warnf(msg string, args ...any) {
	if l == nil {
		slog.Warn(fmt.Sprintf(msg, args...), slog.Any("callstack", Callstack()))
	} else {
		l.Logger.Warn(fmt.Sprintf(msg, args...), slog.Any("callstack", Callstack()))
	}
}

func (l *Logger) Error(msg string, args ...any) {
	args = append([]any{slog.Any("callstack", Callstack())}, args...)
	if l == nil {
		slog.Error(msg, args...)
	} else {
		l.Logger.Error(msg, args...)
	}
}

func (l *Logger) Errorf(msg string, args ...any) {
	if l == nil {
		slog.Error(fmt.Sprintf(msg, args...), slog.Any("callstack", Callstack()))
	} else {
		l.Logger.Error(fmt.Sprintf(msg, args...), slog.Any("callstack", Callstack()))
	}
}

func (l *Logger) With(args ...any) *Logger {
	return &Logger{
		Logger:  l.Logger.With(args...),
		logFile: l.logFile,
		start:   l.start,
	}
}

// Stats collects a few statistics related to rendering and time spent in
// various phases of the system.
type Stats struct {
	render    RendererStats
	renderUI  RendererStats
	drawImgui time.Duration
	drawPanes time.Duration
	startTime time.Time
	redraws   int
}

var startupMallocs uint64

func (stats Stats) LogValue() slog.Value {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	if startupMallocs == 0 { // first call
		startupMallocs = mem.Mallocs
	}

	elapsed := time.Since(lg.start).Seconds()
	mallocsPerSecond := float64(mem.Mallocs-startupMallocs) / elapsed

	return slog.GroupValue(
		slog.Float64("redraws_per_second", float64(stats.redraws)/time.Since(stats.startTime).Seconds()),
		slog.Float64("mallocs_per_second", mallocsPerSecond),
		slog.Int64("active_mallocs", int64(mem.Mallocs-mem.Frees)),
		slog.Int64("memory_in_use", int64(mem.HeapAlloc)),
		slog.Duration("draw_panes", stats.drawPanes),
		slog.Duration("draw_imgui", stats.drawImgui),
		slog.Any("render", stats.render),
		slog.Any("ui", stats.renderUI))
}
