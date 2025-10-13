package logger_test

import (
	"bytes"
	"testing"

	"github.com/rs/zerolog"

	"github.com/example/messaging-microservice/internal/logger"
)

func TestNewSetsGlobalLevel(t *testing.T) {
	cases := map[string]zerolog.Level{
		"":         zerolog.InfoLevel,
		"debug":    zerolog.DebugLevel,
		"Warn":     zerolog.WarnLevel,
		"ERROR":    zerolog.ErrorLevel,
		"fatal":    zerolog.FatalLevel,
		"panic":    zerolog.PanicLevel,
		"disabled": zerolog.Disabled,
	}

	for input, want := range cases {
		input := input
		want := want
		t.Run("level_"+input, func(t *testing.T) {
			prev := zerolog.GlobalLevel()
			t.Cleanup(func() {
				zerolog.SetGlobalLevel(prev)
			})

			var buf bytes.Buffer
			_, err := logger.New("production", input, &buf)
			if err != nil {
				t.Fatalf("New returned error for level %q: %v", input, err)
			}

			if got := zerolog.GlobalLevel(); got != want {
				t.Fatalf("global level = %s, want %s", got, want)
			}
		})
	}
}

func TestNewInvalidLevel(t *testing.T) {
	prev := zerolog.GlobalLevel()
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(prev)
	})

	if _, err := logger.New("production", "not-a-level"); err == nil {
		t.Fatalf("expected error for invalid level")
	}
}
