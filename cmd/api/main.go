// Command api is the service entry point. It does four things and delegates
// everything else to internal/bootstrap: load the environment, start the
// logger, wire the app, run it.
package main

import (
	"context"
	"os"

	"github.com/eandstravel/carwash/internal/bootstrap"
	"github.com/eandstravel/carwash/internal/config"
	"github.com/eandstravel/carwash/pkg/logger"
	"github.com/joho/godotenv"
	"go.uber.org/zap"
)

func main() {
	_ = godotenv.Load()

	cfg := config.Load()
	logger.Init(string(cfg.AppEnv))
	defer logger.Sync()

	app, err := bootstrap.New(context.Background(), cfg, logger.Log)
	if err != nil {
		// Configuration problems arrive as one list rather than one restart
		// at a time (see config.Validate), so this line is usually the only
		// thing an operator needs in order to fix a fresh environment.
		logger.Log.Error("startup failed", zap.Error(err))
		_ = logger.Log.Sync()
		os.Exit(1)
	}

	if err := app.Run(); err != nil {
		logger.Log.Error("server stopped unexpectedly", zap.Error(err))
		_ = logger.Log.Sync()
		os.Exit(1)
	}
}
