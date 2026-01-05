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

import "time"

// Logger provides structured logging that works with both glog and zerolog backends. When zerolog is enabled via the
// --structured-logging flag, log output is JSON formatted for log aggregation systems. When glog is active (the
// default), structured fields are appended as key=value pairs to maintain backward compatibility.
type Logger interface {
	// Info returns an Event for logging at info level.
	Info() *Event

	// Warn returns an Event for logging at warning level.
	Warn() *Event

	// Error returns an Event for logging at error level.
	Error() *Event

	// Fatal returns an Event for logging at fatal level. After the message is logged, the program terminates.
	Fatal() *Event

	// Exit returns an Event for logging at exit level. After the message is logged, the program terminates.
	Exit() *Event

	// V returns a verbose logger. In glog mode, this respects the --v flag and only logs if the configured verbosity
	// level is at least the specified level. In zerolog mode, verbose logs are emitted at debug level with a "v"
	// field indicating the verbosity level.
	V(level Level) Logger

	// With creates a child logger with persistent fields. Fields added via the returned Context will be included in
	// all log events created by the child logger.
	With() *Context
}

// Context builds persistent fields for a child logger. Call Logger() to obtain the child logger with the configured
// fields.
type Context struct {
	// gctx holds the glog context state when glog backend is active.
	gctx *glogContext

	// zctx will hold zerolog.Context when zerolog backend is active (added in Phase 7).
}

// Str adds a string field to the context.
func (c *Context) Str(key, val string) *Context {
	if c.gctx != nil {
		c.gctx.Str(key, val)
	}
	return c
}

// Int adds an int field to the context.
func (c *Context) Int(key string, val int) *Context {
	if c.gctx != nil {
		c.gctx.Int(key, val)
	}
	return c
}

// Int64 adds an int64 field to the context.
func (c *Context) Int64(key string, val int64) *Context {
	if c.gctx != nil {
		c.gctx.Int64(key, val)
	}
	return c
}

// Uint64 adds a uint64 field to the context.
func (c *Context) Uint64(key string, val uint64) *Context {
	if c.gctx != nil {
		c.gctx.Uint64(key, val)
	}
	return c
}

// Bool adds a bool field to the context.
func (c *Context) Bool(key string, val bool) *Context {
	if c.gctx != nil {
		c.gctx.Bool(key, val)
	}
	return c
}

// Err adds an error field to the context. If err is nil, no field is added.
func (c *Context) Err(err error) *Context {
	if c.gctx != nil {
		c.gctx.Err(err)
	}
	return c
}

// Dur adds a duration field to the context.
func (c *Context) Dur(key string, val time.Duration) *Context {
	if c.gctx != nil {
		c.gctx.Dur(key, val)
	}
	return c
}

// Any adds a field of any type to the context. The value is formatted using fmt.Sprintf("%v", val).
func (c *Context) Any(key string, val any) *Context {
	if c.gctx != nil {
		c.gctx.Any(key, val)
	}
	return c
}

// Logger returns a child Logger with the configured persistent fields.
func (c *Context) Logger() Logger {
	if c.gctx != nil {
		return c.gctx.Logger()
	}
	return nil
}

// Event represents a log event being built. When zerolog is active, it wraps zerolog.Event directly for minimal
// overhead. When glog is active, it uses glogEvent for key=value formatting. All field methods return the Event to
// allow method chaining.
type Event struct {
	// gl holds the glog event state when glog backend is active.
	gl *glogEvent

	// zl will hold *zerolog.Event when zerolog backend is active (added in Phase 7).
}

// Str adds a string field to the event.
func (e *Event) Str(key, val string) *Event {
	if e.gl != nil {
		e.gl.Str(key, val)
	}
	return e
}

// Int adds an int field to the event.
func (e *Event) Int(key string, val int) *Event {
	if e.gl != nil {
		e.gl.Int(key, val)
	}
	return e
}

// Int64 adds an int64 field to the event.
func (e *Event) Int64(key string, val int64) *Event {
	if e.gl != nil {
		e.gl.Int64(key, val)
	}
	return e
}

// Uint64 adds a uint64 field to the event.
func (e *Event) Uint64(key string, val uint64) *Event {
	if e.gl != nil {
		e.gl.Uint64(key, val)
	}
	return e
}

// Bool adds a bool field to the event.
func (e *Event) Bool(key string, val bool) *Event {
	if e.gl != nil {
		e.gl.Bool(key, val)
	}
	return e
}

// Err adds an error field to the event. If err is nil, no field is added.
func (e *Event) Err(err error) *Event {
	if e.gl != nil {
		e.gl.Err(err)
	}
	return e
}

// Dur adds a duration field to the event.
func (e *Event) Dur(key string, val time.Duration) *Event {
	if e.gl != nil {
		e.gl.Dur(key, val)
	}
	return e
}

// Any adds a field of any type to the event. The value is formatted using fmt.Sprintf("%v", val).
func (e *Event) Any(key string, val any) *Event {
	if e.gl != nil {
		e.gl.Any(key, val)
	}
	return e
}

// Msg logs the event with the given message. This is a terminal method that completes the log event.
func (e *Event) Msg(msg string) {
	if e.gl != nil {
		e.gl.Msg(msg)
	}
}

// Msgf logs the event with a formatted message. This is a terminal method that completes the log event.
func (e *Event) Msgf(format string, args ...any) {
	if e.gl != nil {
		e.gl.Msgf(format, args...)
	}
}

// MsgDepth logs the event with the given message, adjusting the caller depth for correct file and line reporting.
// This is useful for wrapper functions that need to report the caller's location rather than their own.
func (e *Event) MsgDepth(depth int, msg string) {
	if e.gl != nil {
		e.gl.MsgDepth(depth, msg)
	}
}

// MsgfDepth logs the event with a formatted message, adjusting the caller depth for correct file and line reporting.
// This is useful for wrapper functions that need to report the caller's location rather than their own.
func (e *Event) MsgfDepth(depth int, format string, args ...any) {
	if e.gl != nil {
		e.gl.MsgfDepth(depth, format, args...)
	}
}
