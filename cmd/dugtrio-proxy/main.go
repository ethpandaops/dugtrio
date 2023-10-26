package main

import (
	"flag"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/urfave/negroni"

	"github.com/ethpandaops/dugtrio/frontend"
	"github.com/ethpandaops/dugtrio/frontend/handlers"
	"github.com/ethpandaops/dugtrio/pool"
	"github.com/ethpandaops/dugtrio/proxy"
	"github.com/ethpandaops/dugtrio/types"
	"github.com/ethpandaops/dugtrio/utils"
)

func main() {
	configPath := flag.String("config", "", "Path to the config file, if empty string defaults will be used")
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

	// init proxy handler
	beaconProxy, err := proxy.NewBeaconProxy(config.Proxy, beaconPool)
	if err != nil {
		logrus.Fatalf("error initializing beacon proxy: %v", err)
	}

	// init router
	router := mux.NewRouter()
	router.PathPrefix("/eth/").Handler(beaconProxy)

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
		frontendHandler := handlers.NewFrontendHandler(beaconPool)
		router.HandleFunc("/health", frontendHandler.Health).Methods("GET")

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

	if config.WriteTimeout == 0 {
		config.WriteTimeout = time.Second * 15
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = time.Second * 15
	}
	if config.IdleTimeout == 0 {
		config.IdleTimeout = time.Second * 60
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
