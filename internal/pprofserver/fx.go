package pprofserver

import (
	"go.uber.org/fx"

	"github.com/temporalio/temporal-proxy/internal/api"
	"github.com/temporalio/temporal-proxy/internal/config"
	"github.com/temporalio/temporal-proxy/internal/httpserver"
	"github.com/temporalio/temporal-proxy/pkg/logger"
)

// Module contributes the pprof routes to the shared HTTP server in
// [httpserver.Module]. Include it unconditionally: a disabled pprof block
// contributes no routes, so the module is inert.
var Module = fx.Provide(httpserver.AsRoutes(newFromParams))

// Params collects the fx-provided dependencies the pprof routes need. Every
// field is required. Conns is used only to resolve an extension-server
// authenticator and may be empty otherwise.
type Params struct {
	fx.In

	Config *config.Config
	Conns  api.Connections
	Logger logger.Logger
}
