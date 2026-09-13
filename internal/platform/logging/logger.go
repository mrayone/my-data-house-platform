// Package logging fornece o logger estruturado padrão da plataforma.
//
// Todo componente (dhctl, producer, api) deve obter seu logger por aqui, nunca
// instanciar slog diretamente, para que o formato (JSON) e o nível (via
// LOG_LEVEL) sejam consistentes em todo o repositório.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// New cria um *slog.Logger em formato JSON, escrevendo em os.Stderr.
//
// O nível é lido da variável de ambiente LOG_LEVEL (debug|info|warn|error,
// case-insensitive). Um valor ausente ou não reconhecido usa "info" como
// default silencioso — LOG_LEVEL não é uma variável obrigatória.
func New() *slog.Logger {
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: levelFromEnv(os.Getenv("LOG_LEVEL")),
	})
	return slog.New(handler)
}

func levelFromEnv(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "info", "":
		return slog.LevelInfo
	default:
		return slog.LevelInfo
	}
}
