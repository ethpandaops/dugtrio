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
	configPath := flag.String("config", "dugtrio-config.yaml", "Path to the config file, if empty string defaults will be used")
	flag.Parse()

	config := &types.Config{}
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
			logrus.Errorf("error adding endpoint %v: %v", utils.GetRedactedUrl(endpoint.Url), err)
		}
	}

	// init router
	router := mux.NewRouter()

	// init metrics
	var proxyMetrics *metrics.ProxyMetrics
	if config.Metrics.Enabled {
		proxyMetrics = metrics.NewProxyMetrics(beaconPool)
		router.Path("/metrics").Handler(promhttp.Handler())
	}

	// init proxy handler
	beaconProxy, err := proxy.NewBeaconProxy(config.Proxy, beaconPool, proxyMetrics)
	if err != nil {
		logrus.Fatalf("error initializing beacon proxy: %v", err)
	}

	// standardized beacon node endpoints
	router.PathPrefix("/eth/").Handler(beaconProxy)

	// client specific endpoints
	router.PathPrefix("/lighthouse/").Handler(beaconProxy.NewClientSpecificProxy(pool.LighthouseClient))
	router.PathPrefix("/lodestar/").Handler(beaconProxy.NewClientSpecificProxy(pool.LodestarClient))
	router.PathPrefix("/nimbus/").Handler(beaconProxy.NewClientSpecificProxy(pool.NimbusClient))
	router.PathPrefix("/prysm/").Handler(beaconProxy.NewClientSpecificProxy(pool.PrysmClient))
	router.PathPrefix("/teku/").Handler(beaconProxy.NewClientSpecificProxy(pool.TekuClient))
	router.PathPrefix("/grandine/").Handler(beaconProxy.NewClientSpecificProxy(pool.GrandineClient))

	// healthcheck endpoint
	router.HandleFunc("/healthcheck", beaconProxy.ServeHealthCheckHTTP).Methods("GET")

	if config.Frontend.Pprof {
		// add pprof handler
		router.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)
	}
	if config.Frontend.Enabled {
		frontend, err := frontend.NewFrontend(config.Frontend)
		if err != nil {
			logrus.Fatalf("error initializing frontend: %v", err)
		}

		// register frontend routes
		frontendHandler := handlers.NewFrontendHandler(beaconPool, beaconProxy)
		router.HandleFunc("/", frontendHandler.Index).Methods("GET")
		router.HandleFunc("/health", frontendHandler.Health).Methods("GET")
		router.HandleFunc("/sessions", frontendHandler.Sessions).Methods("GET")

		router.PathPrefix("/").Handler(frontend)
	}

	// start http server
	startHttpServer(config.Server, router)
}

func startHttpServer(config *types.ServerConfig, router *mux.Router) {
	n := negroni.New()
	n.Use(negroni.NewRecovery())
	//n.Use(gzip.Gzip(gzip.DefaultCompression))
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
