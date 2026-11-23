package config

import (
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"time"

	"github.com/caarlos0/env/v11"
)

type Log struct {
	Format Format     `env:"FORMAT" envDefault:"json"`
	Level  slog.Level `env:"LEVEL" envDefault:"info"`
	Source bool       `env:"SOURCE" envDefault:"false"`
}

type Token struct {
	TTL    time.Duration `env:"TTL" envDefault:"12h"`
	Prefix string        `env:"PREFIX" envDefault:"ccr-token-"`
}

type Timeout struct {
	Request  time.Duration `env:"REQUEST" envDefault:"30s"`
	Shutdown time.Duration `env:"SHUTDOWN" envDefault:"25s"`
}

type Server struct {
	Debug    bool    `env:"DEBUG" envDefault:"false"`
	Host     string  `env:"HOST" envDefault:""`
	Port     uint16  `env:"PORT" envDefault:"5001"`
	BasePath string  `env:"BASE_PATH" envDefault:"/api/v1"`
	Timeout  Timeout `envPrefix:"TIMEOUT_"`
}

func (server Server) Address() string {
	return net.JoinHostPort(server.Host, fmt.Sprintf("%d", server.Port))
}

type Issuer struct {
	Registry string `env:"REGISTRY,required,notEmpty" envDefault:"registry.*.ccr.crusoecloudcompute.com"`
	Server   Server `envPrefix:"SERVER_"`
	Token    Token  `envPrefix:"TOKEN_"`
	Log      Log    `envPrefix:"LOG_"`
}

type Crusoe struct {
	AccessKey Secret `env:"ACCESS_KEY,required,notEmpty"`
	SecretKey Secret `env:"SECRET_KEY,required,notEmpty"`
	Issuer    Issuer `envPrefix:"ISSUER_"`
}

func Load() (*Crusoe, error) {
	cfg := &Crusoe{}
	opts := env.Options{
		Prefix: "CRUSOE_",
		FuncMap: map[reflect.Type]env.ParserFunc{
			reflect.TypeFor[time.Duration](): parseDuration,
		},
	}
	if err := env.ParseWithOptions(cfg, opts); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parseDuration(value string) (any, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return nil, err
	}
	if duration <= 0 {
		return nil, fmt.Errorf("duration %q must be positive", value)
	}
	return duration, nil
}
