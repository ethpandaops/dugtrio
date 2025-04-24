package utils

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ethpandaops/dugtrio/types"
	"github.com/sirupsen/logrus"
)

type LogWriter struct {
	logFile *os.File
}

func InitLogger(config *types.LoggingConfig) *LogWriter {
	logrus.SetOutput(io.Discard) // Send all logs to nowhere by default
	logrus.SetLevel(logrus.TraceLevel)

	logWriter := &LogWriter{}
	outputLevel := getLogLevels(logrus.InfoLevel)

	if config.OutputLevel != "" {
		levelParts := strings.Split(config.OutputLevel, "|")
		if len(levelParts) > 1 {
			outputLevel = []logrus.Level{}

			for _, level := range levelParts {
				logLevel := parseLogLevel(level)
				if logLevel != 9999 {
					outputLevel = append(outputLevel, logLevel)
				}
			}
		} else {
			logLevel := parseLogLevel(levelParts[0])
			if logLevel != 9999 {
				outputLevel = getLogLevels(logLevel)
			} else {
				outputLevel = []logrus.Level{}
			}
		}
	}

	if len(outputLevel) > 0 {
		var writer io.Writer

		if config.OutputStderr {
			writer = os.Stderr
		} else {
			writer = os.Stdout
		}

		logrus.AddHook(&LogWriterHook{
			Writer:    writer,
			LogLevels: outputLevel,
		})
	}

	if config.FilePath != "" {
		fileLevel := getLogLevels(logrus.InfoLevel)

		if config.FileLevel != "" {
			levelParts := strings.Split(config.FileLevel, "|")
			if len(levelParts) > 1 {
				fileLevel = []logrus.Level{}

				for _, level := range levelParts {
					logLevel := parseLogLevel(level)
					if logLevel != 9999 {
						fileLevel = append(fileLevel, logLevel)
					}
				}
			} else {
				logLevel := parseLogLevel(levelParts[0])
				if logLevel != 9999 {
					fileLevel = getLogLevels(logLevel)
				} else {
					fileLevel = []logrus.Level{}
				}
			}
		}

		logrus.Printf("logging to file: %v (%v)\n", config.FilePath, fileLevel)

		f, err := os.OpenFile(config.FilePath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Println("Failed to create logfile" + config.FilePath)
			panic(err)
		}

		logWriter.logFile = f
		logrus.AddHook(&LogWriterHook{ // Send info and debug logs to stdout
			Writer:    f,
			LogLevels: fileLevel,
		})
	}

	return logWriter
}

func (logWriter *LogWriter) Dispose() {
	if logWriter.logFile != nil {
		logWriter.logFile.Close()
		logWriter.logFile = nil
	}
}

func getLogLevels(level logrus.Level) []logrus.Level {
	switch level {
	case logrus.TraceLevel:
		return []logrus.Level{
			logrus.PanicLevel,
			logrus.FatalLevel,
			logrus.ErrorLevel,
			logrus.WarnLevel,
			logrus.InfoLevel,
			logrus.DebugLevel,
			logrus.TraceLevel,
		}
	case logrus.DebugLevel:
		return []logrus.Level{
			logrus.PanicLevel,
			logrus.FatalLevel,
			logrus.ErrorLevel,
			logrus.WarnLevel,
			logrus.InfoLevel,
			logrus.DebugLevel,
		}
	case logrus.InfoLevel:
		return []logrus.Level{
			logrus.PanicLevel,
			logrus.FatalLevel,
			logrus.ErrorLevel,
			logrus.WarnLevel,
			logrus.InfoLevel,
		}
	case logrus.WarnLevel:
		return []logrus.Level{
			logrus.PanicLevel,
			logrus.FatalLevel,
			logrus.ErrorLevel,
			logrus.WarnLevel,
		}
	case logrus.ErrorLevel:
		return []logrus.Level{
			logrus.PanicLevel,
			logrus.FatalLevel,
			logrus.ErrorLevel,
		}
	case logrus.FatalLevel:
		return []logrus.Level{
			logrus.PanicLevel,
			logrus.FatalLevel,
		}
	case logrus.PanicLevel:
		return []logrus.Level{
			logrus.PanicLevel,
		}
	}

	return []logrus.Level{}
}

func parseLogLevel(level string) logrus.Level {
	switch level {
	case "trace":
		return logrus.TraceLevel
	case "debug":
		return logrus.DebugLevel
	case "info":
		return logrus.InfoLevel
	case "warn":
		return logrus.WarnLevel
	case "error":
		return logrus.ErrorLevel
	case "fatal":
		return logrus.FatalLevel
	case "panic":
		return logrus.PanicLevel
	case "none":
		return 9999
	}

	return 0
}

// WriterHook is a hook that writes logs of specified LogLevels to specified Writer
type LogWriterHook struct {
	Writer    io.Writer
	LogLevels []logrus.Level
}

// Fire will be called when some logging function is called with current hook
// It will format log entry to string and write it to appropriate writer
func (hook *LogWriterHook) Fire(entry *logrus.Entry) error {
	line, err := entry.String()
	if err != nil {
		return err
	}

	_, err = hook.Writer.Write([]byte(line))

	return err
}

func (hook *LogWriterHook) Levels() []logrus.Level {
	return hook.LogLevels
}

// LogFatal logs a fatal error with callstack info that skips callerSkip many levels with arbitrarily many additional infos.
// callerSkip equal to 0 gives you info directly where LogFatal is called.
func LogFatal(err error, errorMsg interface{}, callerSkip int, additionalInfos ...map[string]interface{}) {
	logErrorInfo(err, callerSkip, additionalInfos...).Fatal(errorMsg)
}

// LogError logs an error with callstack info that skips callerSkip many levels with arbitrarily many additional infos.
// callerSkip equal to 0 gives you info directly where LogError is called.
func LogError(err error, errorMsg interface{}, callerSkip int, additionalInfos ...map[string]interface{}) {
	logErrorInfo(err, callerSkip, additionalInfos...).Error(errorMsg)
}

func logErrorInfo(err error, callerSkip int, additionalInfos ...map[string]interface{}) *logrus.Entry {
	logFields := logrus.NewEntry(logrus.New())

	pc, fullFilePath, line, ok := runtime.Caller(callerSkip + 2)
	if ok {
		logFields = logFields.WithFields(logrus.Fields{
			"_file":     filepath.Base(fullFilePath),
			"_function": runtime.FuncForPC(pc).Name(),
			"_line":     line,
		})
	} else {
		logFields = logFields.WithField("runtime", "Callstack cannot be read")
	}

	errColl := []string{}

	for {
		errColl = append(errColl, fmt.Sprint(err))

		nextErr := errors.Unwrap(err)
		if nextErr != nil {
			err = nextErr
		} else {
			break
		}
	}

	errMarkSign := "~"
	for idx := 0; idx < (len(errColl) - 1); idx++ {
		errInfoText := fmt.Sprintf("%serrInfo_%v%s", errMarkSign, idx, errMarkSign)
		nextErrInfoText := fmt.Sprintf("%serrInfo_%v%s", errMarkSign, idx+1, errMarkSign)

		if idx == (len(errColl) - 2) {
			nextErrInfoText = fmt.Sprintf("%serror%s", errMarkSign, errMarkSign)
		}

		// Replace the last occurrence of the next error in the current error
		lastIdx := strings.LastIndex(errColl[idx], errColl[idx+1])
		if lastIdx != -1 {
			errColl[idx] = errColl[idx][:lastIdx] + nextErrInfoText + errColl[idx][lastIdx+len(errColl[idx+1]):]
		}

		errInfoText = strings.ReplaceAll(errInfoText, errMarkSign, "")
		logFields = logFields.WithField(errInfoText, errColl[idx])
	}

	if err != nil {
		logFields = logFields.WithField("errType", fmt.Sprintf("%T", err)).WithError(err)
	}

	for _, infoMap := range additionalInfos {
		for name, info := range infoMap {
			logFields = logFields.WithField(name, info)
		}
	}

	return logFields
}

func GetRedactedURL(requrl string) string {
	var logurl string

	urlData, _ := url.Parse(requrl)
	if urlData != nil {
		logurl = urlData.Redacted()
	} else {
		logurl = requrl
	}

	return logurl
}
