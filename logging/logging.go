/*
   Velociraptor - Hunting Evil
   Copyright (C) 2019 Velocidex Innovations.

   This program is free software: you can redistribute it and/or modify
   it under the terms of the GNU Affero General Public License as published
   by the Free Software Foundation, either version 3 of the License, or
   (at your option) any later version.

   This program is distributed in the hope that it will be useful,
   but WITHOUT ANY WARRANTY; without even the implied warranty of
   MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
   GNU Affero General Public License for more details.

   You should have received a copy of the GNU Affero General Public License
   along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/
package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"github.com/pkg/errors"
	"github.com/rifflock/lfshook"
	"github.com/sirupsen/logrus"
	config_proto "www.velocidex.com/golang/velociraptor/config/proto"
)

var (
	SuppressLogging = false

	GenericComponent  = "Velociraptor"
	FrontendComponent = "VelociraptorFrontend"
	ClientComponent   = "VelociraptorClient"
	GUIComponent      = "VelociraptorGUI"
	ToolComponent     = "Velociraptor"
	APICmponent       = "VelociraptorAPI"

	// Used for high value audit related events.
	Audit = "VelociraptorAudit"

	Manager *LogManager
)

type LogContext struct {
	*logrus.Logger
}

func (self *LogContext) Debug(format string, v ...interface{}) {
	self.Logger.Debug(fmt.Sprintf(format, v...))
}

func (self *LogContext) Info(format string, v ...interface{}) {
	self.Logger.Info(fmt.Sprintf(format, v...))
}

func (self *LogContext) Warn(format string, v ...interface{}) {
	self.Logger.Warn(fmt.Sprintf(format, v...))
}

func (self *LogContext) Err(format string, v ...interface{}) {
	self.Logger.Error(fmt.Sprintf(format, v...))
}

type LogManager struct {
	mu       sync.Mutex
	contexts map[*string]*LogContext
}

// Get the logger from cache - creating it if it needs to.
func (self *LogManager) GetLogger(
	config_obj *config_proto.Config,
	component *string) *LogContext {
	self.mu.Lock()
	defer self.mu.Unlock()

	if !config_obj.Logging.SeparateLogsPerComponent {
		component = &GenericComponent
	}

	ctx, pres := self.contexts[component]
	if !pres {
		// Add a new context.
		switch component {
		case &GenericComponent,
			&FrontendComponent, &ToolComponent, &Audit,
			&ClientComponent, &GUIComponent, &APICmponent:

			logger := self.makeNewComponent(config_obj, component)
			if config_obj.Logging.SeparateLogsPerComponent {
				self.contexts[component] = logger
				return logger
			} else {
				self.contexts[&GenericComponent] = logger
				return logger
			}

		default:
			panic("Unsupported component!")
		}
	}
	return ctx
}

func getRotator(
	config_obj *config_proto.Config,
	base_path string) *rotatelogs.RotateLogs {

	max_age := config_obj.Logging.MaxAge
	if max_age == 0 {
		max_age = 86400 * 365 // 1 year.
	}

	rotation := config_obj.Logging.RotationTime
	if rotation == 0 {
		rotation = 604800 // 7 days
	}

	result, err := rotatelogs.New(
		base_path+".%Y%m%d%H%M",
		rotatelogs.WithLinkName(base_path),
		// 365 days.
		rotatelogs.WithMaxAge(time.Duration(max_age)*time.Second),
		// 7 days.
		rotatelogs.WithRotationTime(time.Duration(rotation)*time.Second),
	)

	if err != nil {
		panic(err)
	}

	return result
}

func (self *LogManager) makeNewComponent(
	config_obj *config_proto.Config,
	component *string) *LogContext {

	Log := logrus.New()
	Log.Out = ioutil.Discard
	Log.Level = logrus.DebugLevel

	if config_obj.Logging.OutputDirectory != "" {
		err := os.MkdirAll(config_obj.Logging.OutputDirectory, 0700)
		if err != nil {
			panic("Unable to create logging directory.")
		}

		base_filename := filepath.Join(
			config_obj.Logging.OutputDirectory,
			*component)

		pathMap := lfshook.WriterMap{
			logrus.DebugLevel: getRotator(
				config_obj,
				base_filename+"_debug.log"),
			logrus.InfoLevel: getRotator(
				config_obj,
				base_filename+"_info.log"),
			logrus.ErrorLevel: getRotator(
				config_obj,
				base_filename+"_error.log"),
		}

		hook := lfshook.NewHook(
			pathMap,
			&logrus.JSONFormatter{},
		)
		Log.Hooks.Add(hook)
	}

	stderr_map := lfshook.WriterMap{
		logrus.ErrorLevel: os.Stderr,
	}

	if !SuppressLogging {
		stderr_map[logrus.DebugLevel] = os.Stderr
		stderr_map[logrus.InfoLevel] = os.Stderr
		stderr_map[logrus.WarnLevel] = os.Stderr
		stderr_map[logrus.ErrorLevel] = os.Stderr
	}

	Log.Hooks.Add(lfshook.NewHook(stderr_map, &Formatter{}))

	return &LogContext{Log}
}

type Formatter struct{}

func (self *Formatter) Format(entry *logrus.Entry) ([]byte, error) {
	b := &bytes.Buffer{}

	levelText := strings.ToUpper(entry.Level.String())
	fmt.Fprintf(b, "[%s] %v %s ", levelText, entry.Time.Format(time.RFC3339),
		strings.TrimRight(entry.Message, "\r\n"))

	if len(entry.Data) > 0 {
		serialized, _ := json.Marshal(entry.Data)
		fmt.Fprintf(b, "%s", serialized)
	}

	return append(b.Bytes(), '\n'), nil
}

type logWriter struct {
	logger *LogContext
}

func (self *logWriter) Write(b []byte) (int, error) {
	self.logger.Info("%s", string(b))
	return len(b), nil
}

// A log compatible logger.
func NewPlainLogger(
	config *config_proto.Config,
	component *string) *log.Logger {
	if !SuppressLogging {
		return log.New(&logWriter{
			GetLogger(config, component)}, "", 0)
	}

	return log.New(ioutil.Discard, "", 0)
}

func GetLogger(config_obj *config_proto.Config, component *string) *LogContext {
	return Manager.GetLogger(config_obj, component)
}

type stackTracer interface {
	StackTrace() errors.StackTrace
}

func GetStackTrace(err error) string {
	if err, ok := err.(stackTracer); ok {
		for _, f := range err.StackTrace() {
			return fmt.Sprintf("%+s:%d\n", f, f)
		}
	}
	return ""
}

func init() {
	Manager = &LogManager{
		contexts: make(map[*string]*LogContext),
	}
}
