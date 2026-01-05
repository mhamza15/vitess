/*
Copyright 2019 The Vitess Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package log

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
)

// glogLevel represents the severity level for glog output.
type glogLevel int

const (
	levelInfo glogLevel = iota
	levelWarning
	levelError
	levelFatal
	levelExit
)

// field represents a single key-value pair for structured logging output.
type field struct {
	key   string
	value string
}

// glogEvent accumulates structured fields and formats them as key=value pairs appended to the log message. When Msg()
// or Msgf() is called, the accumulated fields are formatted and passed to the appropriate glog function.
type glogEvent struct {
	level    glogLevel
	fields   []field
	depth    int
	disabled bool
}

// newGlogEvent creates a new glogEvent with the specified severity level. The default depth is set to 2 to account
// for the call chain: user code -> package function -> newGlogEvent.
func newGlogEvent(level glogLevel) *glogEvent {
	return &glogEvent{level: level, depth: 2}
}

// newDisabledGlogEvent creates a disabled glogEvent that silently discards all operations. This is used when V()
// level filtering determines the log should not be emitted.
func newDisabledGlogEvent() *glogEvent {
	return &glogEvent{disabled: true}
}

// Str adds a string field to the event.
func (e *glogEvent) Str(key, val string) {
	if e.disabled {
		return
	}
	e.fields = append(e.fields, field{key, val})
}

// Int adds an int field to the event.
func (e *glogEvent) Int(key string, val int) {
	if e.disabled {
		return
	}
	e.fields = append(e.fields, field{key, strconv.Itoa(val)})
}

// Int64 adds an int64 field to the event.
func (e *glogEvent) Int64(key string, val int64) {
	if e.disabled {
		return
	}
	e.fields = append(e.fields, field{key, strconv.FormatInt(val, 10)})
}

// Uint64 adds a uint64 field to the event.
func (e *glogEvent) Uint64(key string, val uint64) {
	if e.disabled {
		return
	}
	e.fields = append(e.fields, field{key, strconv.FormatUint(val, 10)})
}

// Bool adds a bool field to the event.
func (e *glogEvent) Bool(key string, val bool) {
	if e.disabled {
		return
	}
	e.fields = append(e.fields, field{key, strconv.FormatBool(val)})
}

// Err adds an error field to the event. If err is nil, no field is added.
func (e *glogEvent) Err(err error) {
	if e.disabled || err == nil {
		return
	}
	e.fields = append(e.fields, field{"error", err.Error()})
}

// Dur adds a duration field to the event.
func (e *glogEvent) Dur(key string, val time.Duration) {
	if e.disabled {
		return
	}
	e.fields = append(e.fields, field{key, val.String()})
}

// Any adds a field of any type to the event. The value is formatted using fmt.Sprintf("%v", val).
func (e *glogEvent) Any(key string, val any) {
	if e.disabled {
		return
	}
	e.fields = append(e.fields, field{key, fmt.Sprintf("%v", val)})
}

// Msg logs the event with the given message. The message and all accumulated fields are formatted as "message
// key1=value1 key2=value2" and passed to the appropriate glog function based on the event's severity level.
func (e *glogEvent) Msg(msg string) {
	if e.disabled {
		return
	}
	formatted := e.formatMessage(msg)
	switch e.level {
	case levelInfo:
		glog.InfoDepth(e.depth, formatted)
	case levelWarning:
		glog.WarningDepth(e.depth, formatted)
	case levelError:
		glog.ErrorDepth(e.depth, formatted)
	case levelFatal:
		glog.FatalDepth(e.depth, formatted)
	case levelExit:
		glog.ExitDepth(e.depth, formatted)
	}
}

// Msgf logs the event with a formatted message.
func (e *glogEvent) Msgf(format string, args ...any) {
	if e.disabled {
		return
	}
	e.Msg(fmt.Sprintf(format, args...))
}

// MsgDepth logs the event with the given message, adjusting the caller depth.
func (e *glogEvent) MsgDepth(depth int, msg string) {
	e.depth += depth
	e.Msg(msg)
}

// MsgfDepth logs the event with a formatted message, adjusting the caller depth.
func (e *glogEvent) MsgfDepth(depth int, format string, args ...any) {
	e.depth += depth
	e.Msgf(format, args...)
}

// formatMessage builds the final log message with all accumulated fields appended as key=value pairs.
func (e *glogEvent) formatMessage(msg string) string {
	if len(e.fields) == 0 {
		return msg
	}
	var buf strings.Builder
	buf.WriteString(msg)
	for _, f := range e.fields {
		buf.WriteByte(' ')
		buf.WriteString(f.key)
		buf.WriteByte('=')
		buf.WriteString(f.value)
	}
	return buf.String()
}

// glogContext accumulates persistent fields for creating child loggers. When Logger() is called, it returns a
// glogLogger that includes these fields in all log events.
type glogContext struct {
	fields []field
}

// newGlogContext creates a new glogContext, optionally copying fields from a parent context.
func newGlogContext(parent []field) *glogContext {
	ctx := &glogContext{}
	if len(parent) > 0 {
		ctx.fields = make([]field, len(parent))
		copy(ctx.fields, parent)
	}
	return ctx
}

// Str adds a string field to the context.
func (c *glogContext) Str(key, val string) {
	c.fields = append(c.fields, field{key, val})
}

// Int adds an int field to the context.
func (c *glogContext) Int(key string, val int) {
	c.fields = append(c.fields, field{key, strconv.Itoa(val)})
}

// Int64 adds an int64 field to the context.
func (c *glogContext) Int64(key string, val int64) {
	c.fields = append(c.fields, field{key, strconv.FormatInt(val, 10)})
}

// Uint64 adds a uint64 field to the context.
func (c *glogContext) Uint64(key string, val uint64) {
	c.fields = append(c.fields, field{key, strconv.FormatUint(val, 10)})
}

// Bool adds a bool field to the context.
func (c *glogContext) Bool(key string, val bool) {
	c.fields = append(c.fields, field{key, strconv.FormatBool(val)})
}

// Err adds an error field to the context. If err is nil, no field is added.
func (c *glogContext) Err(err error) {
	if err == nil {
		return
	}
	c.fields = append(c.fields, field{"error", err.Error()})
}

// Dur adds a duration field to the context.
func (c *glogContext) Dur(key string, val time.Duration) {
	c.fields = append(c.fields, field{key, val.String()})
}

// Any adds a field of any type to the context.
func (c *glogContext) Any(key string, val any) {
	c.fields = append(c.fields, field{key, fmt.Sprintf("%v", val)})
}

// Logger returns a new glogLogger with the accumulated fields.
func (c *glogContext) Logger() Logger {
	return &glogLogger{fields: c.fields}
}

// glogLogger implements the Logger interface using glog as the backend. It maintains a set of persistent fields that
// are included in all log events created by this logger.
type glogLogger struct {
	fields []field
}

// newGlogLogger creates a new glogLogger with no persistent fields.
func newGlogLogger() *glogLogger {
	return &glogLogger{}
}

// Info returns an Event for logging at info level.
func (l *glogLogger) Info() *Event {
	return l.newEvent(levelInfo)
}

// Warn returns an Event for logging at warning level.
func (l *glogLogger) Warn() *Event {
	return l.newEvent(levelWarning)
}

// Error returns an Event for logging at error level.
func (l *glogLogger) Error() *Event {
	return l.newEvent(levelError)
}

// Fatal returns an Event for logging at fatal level.
func (l *glogLogger) Fatal() *Event {
	return l.newEvent(levelFatal)
}

// Exit returns an Event for logging at exit level.
func (l *glogLogger) Exit() *Event {
	return l.newEvent(levelExit)
}

// V returns a verbose logger that only emits logs if the configured verbosity level is at least the specified level.
// This respects glog's --v flag.
func (l *glogLogger) V(level Level) Logger {
	if !glog.V(level) {
		return &disabledGlogLogger{}
	}
	return &verboseGlogLogger{fields: l.fields}
}

// With creates a Context for building a child logger with persistent fields.
func (l *glogLogger) With() *Context {
	return &Context{gctx: newGlogContext(l.fields)}
}

// newEvent creates a new Event with the logger's persistent fields pre-populated.
func (l *glogLogger) newEvent(level glogLevel) *Event {
	e := newGlogEvent(level)
	if len(l.fields) > 0 {
		e.fields = make([]field, len(l.fields))
		copy(e.fields, l.fields)
	}
	return &Event{gl: e}
}

// disabledGlogLogger is a Logger implementation that silently discards all log operations. It is returned by V() when
// the verbosity level check fails.
type disabledGlogLogger struct{}

func (l *disabledGlogLogger) Info() *Event         { return &Event{gl: newDisabledGlogEvent()} }
func (l *disabledGlogLogger) Warn() *Event         { return &Event{gl: newDisabledGlogEvent()} }
func (l *disabledGlogLogger) Error() *Event        { return &Event{gl: newDisabledGlogEvent()} }
func (l *disabledGlogLogger) Fatal() *Event        { return &Event{gl: newDisabledGlogEvent()} }
func (l *disabledGlogLogger) Exit() *Event         { return &Event{gl: newDisabledGlogEvent()} }
func (l *disabledGlogLogger) V(level Level) Logger { return l }
func (l *disabledGlogLogger) With() *Context       { return &Context{gctx: newGlogContext(nil)} }

// verboseGlogLogger is a Logger implementation for V() calls that passed the verbosity check. It logs at info level.
type verboseGlogLogger struct {
	fields []field
}

func (l *verboseGlogLogger) Info() *Event {
	e := newGlogEvent(levelInfo)
	if len(l.fields) > 0 {
		e.fields = make([]field, len(l.fields))
		copy(e.fields, l.fields)
	}
	return &Event{gl: e}
}

func (l *verboseGlogLogger) Warn() *Event  { return l.Info() }
func (l *verboseGlogLogger) Error() *Event { return l.Info() }
func (l *verboseGlogLogger) Fatal() *Event { return l.Info() }
func (l *verboseGlogLogger) Exit() *Event  { return l.Info() }
func (l *verboseGlogLogger) V(level Level) Logger {
	if !glog.V(level) {
		return &disabledGlogLogger{}
	}
	return l
}
func (l *verboseGlogLogger) With() *Context { return &Context{gctx: newGlogContext(l.fields)} }
