package main

import (
	"flag"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/urfave/negroni"

	"github.com/ethpandaops/dugtrio/frontend"
	"github.com/ethpandaops/dugtrio/frontend/handlers"
	"github.com/ethpandaops/dugtrio/metrics"
	"github.com/ethpandaops/dugtrio/pool"
	"github.com/ethpandaops/dugtrio/proxy"
	"github.com/ethpandaops/dugtrio/types"
	"github.com/ethpandaops/dugtrio/utils"
)

func main() {
	config := &types.Config{}
	configPath := flag.String("config", "dugtrio-config.yaml", "Path to the config file, if empty string defaults will be used")

	flag.Parse()

	err := utils.ReadConfig(config, *configPath)
	if err != nil {
		logrus.Fatalf("error reading config file: %v", err)
	}

	logWriter := utils.InitLogger(config.Logging)
	defer logWriter.Dispose()

	logrus.WithFields(logrus.Fields{
		"config":  *configPath,
		"version": utils.BuildVersion,
		"release": utils.BuildRelease,
	}).Printf("starting")

	startDugtrio(config)

	utils.WaitForCtrlC()
	logrus.Println("exiting...")
}

func startDugtrio(config *types.Config) {
	// init pool
	beaconPool, err := pool.NewBeaconPool(config.Pool)
	if err != nil {
		logrus.Fatalf("error initializing beacon pool: %v", err)
	}

	// add endpoints to pool
	for _, endpoint := range config.Endpoints {
		_, err := beaconPool.AddEndpoint(endpoint)
		if err != nil {
			logrus.Errorf("error adding endpoint %v: %v", utils.GetRedactedURL(endpoint.URL), err)
		}
	}

	// init router
	router := mux.NewRouter()

	// init metrics
	var proxyMetrics *metrics.ProxyMetrics
	if config.Metrics != nil && config.Metrics.Enabled {
		proxyMetrics = metrics.NewProxyMetrics(beaconPool)

		router.Path("/metrics").Handler(promhttp.Handler())
	}

	// init proxy handler
	beaconProxy, err := proxy.NewBeaconProxy(config.Proxy, beaconPool, proxyMetrics)
	if err != nil {
		logrus.Fatalf("error initializing beacon proxy: %v", err)
	}

	requireTokens := config.Proxy != nil && config.Proxy.RequireTokens

	// standardized beacon node endpoints
	router.PathPrefix("/eth/").Handler(requireTokensGuard(requireTokens, beaconProxy))

	// client specific endpoints
	router.PathPrefix("/caplin/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.CaplinClient)))
	router.PathPrefix("/grandine/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.GrandineClient)))
	router.PathPrefix("/lighthouse/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.LighthouseClient)))
	router.PathPrefix("/lodestar/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.LodestarClient)))
	router.PathPrefix("/nimbus/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.NimbusClient)))
	router.PathPrefix("/prysm/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.PrysmClient)))
	router.PathPrefix("/teku/").Handler(requireTokensGuard(requireTokens, beaconProxy.NewClientSpecificProxy(pool.TekuClient)))

	/* liveness endpoint - always 200 while the process is running */
	router.HandleFunc("/livez", beaconProxy.ServeLivezHTTP).Methods("GET")

	// healthcheck endpoint
	router.HandleFunc("/healthcheck", beaconProxy.ServeHealthCheckHTTP).Methods("GET")

	if config.Frontend != nil && config.Frontend.Pprof {
		// add pprof handler
		router.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)
	}

	hasClients := config.Proxy != nil && len(config.Proxy.Clients) > 0

	if hasClients {
		/* Token router: registered after specific prefixes so they take priority.
		   When the frontend is also active, exact page routes are registered first,
		   then the token router catches everything else and falls back to the static
		   file handler so /static/, /webfonts/, favicon etc. still resolve. */
		tokenRouter := proxy.NewTokenRouter(beaconProxy, config.Proxy.Clients)

		if config.Frontend != nil && config.Frontend.Enabled {
			frontendBaseHandler, err := frontend.NewFrontend(config.Frontend)
			if err != nil {
				logrus.Fatalf("error initializing frontend: %v", err)
			}

			/* Exact frontend page routes must be registered BEFORE the PathPrefix("/")
			   catch-all — gorilla/mux matches in registration order, first match wins. */
			frontendHandler := handlers.NewFrontendHandler(beaconPool, beaconProxy)
			router.HandleFunc("/", frontendHandler.Index).Methods("GET")
			router.HandleFunc("/health", frontendHandler.Health).Methods("GET")
			router.HandleFunc("/sessions", frontendHandler.Sessions).Methods("GET")

			router.PathPrefix("/").Handler(tokenRouter.WithFallback(frontendBaseHandler))
		} else {
			router.PathPrefix("/").Handler(tokenRouter)
		}
	} else if config.Frontend != nil && config.Frontend.Enabled {
		/* No token routing — register frontend as the plain catch-all. */
		frontendBaseHandler, err := frontend.NewFrontend(config.Frontend)
		if err != nil {
			logrus.Fatalf("error initializing frontend: %v", err)
		}

		frontendHandler := handlers.NewFrontendHandler(beaconPool, beaconProxy)
		router.HandleFunc("/", frontendHandler.Index).Methods("GET")
		router.HandleFunc("/health", frontendHandler.Health).Methods("GET")
		router.HandleFunc("/sessions", frontendHandler.Sessions).Methods("GET")
		router.PathPrefix("/").Handler(frontendBaseHandler)
	}

	// start http server
	startHTTPServer(config.Server, router)
}

// requireTokensGuard wraps h to return 401 when require_tokens is enabled,
// blocking legacy direct-access routes during Phase 3 migration.
func requireTokensGuard(required bool, h http.Handler) http.Handler {
	if !required {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusUnauthorized)

		if _, err := w.Write([]byte("Unauthorized")); err != nil {
			logrus.Warnf("error writing unauthorized response: %v", err)
		}
	})
}

func startHTTPServer(config *types.ServerConfig, router *mux.Router) {
	n := negroni.New()
	n.Use(negroni.NewRecovery())
	n.UseHandler(router)

	if config.Host == "" {
		config.Host = "0.0.0.0"
	}

	if config.Port == "" {
		config.Port = "8080"
	}

	srv := &http.Server{
		Addr:         config.Host + ":" + config.Port,
		WriteTimeout: config.WriteTimeout,
		ReadTimeout:  config.ReadTimeout,
		IdleTimeout:  config.IdleTimeout,
		Handler:      n,
	}

	logrus.Printf("http server listening on %v", srv.Addr)

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			logrus.WithError(err).Fatal("Error serving frontend")
		}
	}()
}
