package logger

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

const simpleTimeFormat = "02-01-2006 15:04:05"

// New constructs a zerolog logger according to the runtime environment.
// Development environments receive human readable console logs while other
// environments emit JSON for easy ingestion.
func New(env, level string, writers ...io.Writer) (*zerolog.Logger, error) {
	lvl, err := parseLevel(level)
	if err != nil {
		return nil, err
	}
	zerolog.SetGlobalLevel(lvl)
	zerolog.TimeFieldFormat = simpleTimeFormat
	zerolog.DurationFieldUnit = time.Millisecond

	var output io.Writer
	if len(writers) > 0 {
		output = io.MultiWriter(writers...)
	} else if strings.EqualFold(env, "development") || strings.EqualFold(env, "dev") {
		cw := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: simpleTimeFormat}
		cw.FieldsExclude = []string{zerolog.TimestampFieldName}
		output = cw
	} else {
		output = os.Stdout
	}

	logger := zerolog.New(output).With().Timestamp().Logger().Level(lvl)
	return &logger, nil
}

func parseLevel(level string) (zerolog.Level, error) {
	level = strings.TrimSpace(level)
	if level == "" {
		level = zerolog.InfoLevel.String()
	}
	lvl, err := zerolog.ParseLevel(strings.ToLower(level))
	if err != nil {
		return zerolog.NoLevel, err
	}
	return lvl, nil
}
