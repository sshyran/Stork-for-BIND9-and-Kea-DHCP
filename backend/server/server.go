package server

import (
	"errors"
	"fmt"
	"os"

	flags "github.com/jessevdk/go-flags"
	log "github.com/sirupsen/logrus"

	"isc.org/stork"
	"isc.org/stork/server/agentcomm"
	"isc.org/stork/server/apps"
	"isc.org/stork/server/apps/bind9"
	"isc.org/stork/server/apps/kea"
	"isc.org/stork/server/certs"
	"isc.org/stork/server/configreview"
	dbops "isc.org/stork/server/database"
	dbmodel "isc.org/stork/server/database/model"
	"isc.org/stork/server/eventcenter"
	"isc.org/stork/server/metrics"
	"isc.org/stork/server/restservice"
)

// Global Stork Server state.
type StorkServer struct {
	DBSettings dbops.DatabaseSettings
	DB         *dbops.PgDB

	AgentsSettings agentcomm.AgentsSettings
	Agents         agentcomm.ConnectedAgents

	RestAPISettings restservice.RestAPISettings
	RestAPI         *restservice.RestAPI

	Pullers *apps.Pullers

	EnableMetricsEndpoint bool
	MetricsCollector      metrics.Collector

	EventCenter eventcenter.EventCenter

	ReviewDispatcher configreview.Dispatcher
}

// Global server settings (called application settings in go-flags nomenclature).
type Settings struct {
	Version               bool `short:"v" long:"version" description:"show software version"`
	EnableMetricsEndpoint bool `short:"m" long:"metrics" description:"enable Prometheus /metrics endpoint (no auth)" env:"STORK_SERVER_ENABLE_METRICS"`
}

func (ss *StorkServer) ParseArgs() {
	// Process command line flags.
	var serverSettings Settings
	parser := flags.NewParser(&serverSettings, flags.Default)
	parser.ShortDescription = "Stork Server"
	parser.LongDescription = "Stork Server is a Kea and BIND 9 Dashboard"

	// Process Database specific args.
	_, err := parser.AddGroup("Database ConnectionFlags", "", &ss.DBSettings)
	if err != nil {
		log.Fatalf("FATAL error: %+v", err)
	}

	// Process ReST API specific args.
	_, err = parser.AddGroup("ReST Server Flags", "", &ss.RestAPISettings)
	if err != nil {
		log.Fatalf("FATAL error: %+v", err)
	}

	// Process agent comm specific args.
	_, err = parser.AddGroup("Agents Communication Flags", "", &ss.AgentsSettings)
	if err != nil {
		log.Fatalf("FATAL error: %+v", err)
	}

	// Do args parsing.
	if _, err := parser.Parse(); err != nil {
		code := 1
		var flagsError *flags.Error
		if errors.As(err, &flagsError) {
			if flagsError.Type == flags.ErrHelp {
				code = 0
			}
		}
		os.Exit(code)
	}

	ss.EnableMetricsEndpoint = serverSettings.EnableMetricsEndpoint

	if serverSettings.Version {
		// If user specified --version or -v, print the version and quit.
		fmt.Printf("%s\n", stork.Version)
		os.Exit(0)
	}
}

// Init for Stork Server state.
func NewStorkServer() (ss *StorkServer, err error) {
	ss = &StorkServer{}
	ss.ParseArgs()

	// setup database connection
	ss.DB, err = dbops.NewPgDB(&ss.DBSettings)
	if err != nil {
		return nil, err
	}

	// initialize stork settings
	err = dbmodel.InitializeSettings(ss.DB)
	if err != nil {
		return nil, err
	}

	// prepare certificates for establish secure connections
	// between server and agents
	caCertPEM, serverCertPEM, serverKeyPEM, err := certs.SetupServerCerts(ss.DB)
	if err != nil {
		return nil, err
	}

	// setup event center
	ss.EventCenter = eventcenter.NewEventCenter(ss.DB)

	// setup connected agents
	ss.Agents = agentcomm.NewConnectedAgents(&ss.AgentsSettings, ss.EventCenter, caCertPEM, serverCertPEM, serverKeyPEM)
	// TODO: if any operation below fails then this Shutdown here causes segfault.
	// I do not know why and do not how to fix this. Commenting out for now.
	// defer func() {
	// 	if err != nil {
	// 		ss.Agents.Shutdown()
	// 	}
	// }()

	// Setup configuration review dispatcher.
	ss.ReviewDispatcher = configreview.NewDispatcher(ss.DB)
	configreview.RegisterDefaultCheckers(ss.ReviewDispatcher)
	ss.ReviewDispatcher.Start()

	// initialize stork statistics
	err = dbmodel.InitializeStats(ss.DB)
	if err != nil {
		return nil, err
	}

	ss.Pullers = &apps.Pullers{}

	// setup apps state puller
	ss.Pullers.AppsStatePuller, err = apps.NewStatePuller(ss.DB, ss.Agents, ss.EventCenter, ss.ReviewDispatcher)
	if err != nil {
		return nil, err
	}

	// setup bind9 stats puller
	ss.Pullers.Bind9StatsPuller, err = bind9.NewStatsPuller(ss.DB, ss.Agents, ss.EventCenter)
	if err != nil {
		return nil, err
	}

	// setup kea stats puller
	ss.Pullers.KeaStatsPuller, err = kea.NewStatsPuller(ss.DB, ss.Agents)
	if err != nil {
		return nil, err
	}

	// Setup Kea hosts puller.
	ss.Pullers.KeaHostsPuller, err = kea.NewHostsPuller(ss.DB, ss.Agents, ss.ReviewDispatcher)
	if err != nil {
		return nil, err
	}

	// Setup Kea HA status puller.
	ss.Pullers.HAStatusPuller, err = kea.NewHAStatusPuller(ss.DB, ss.Agents)
	if err != nil {
		return nil, err
	}

	if ss.EnableMetricsEndpoint {
		ss.MetricsCollector, err = metrics.NewCollector(ss.DB)
		if err != nil {
			return nil, err
		}
		log.Info("the metrics endpoint is enabled (ensure that it is properly secured)")
	} else {
		log.Warn("the metric endpoint is disabled (it can be enabled with the -m flag)")
	}

	// setup ReST API service
	r, err := restservice.NewRestAPI(&ss.RestAPISettings, &ss.DBSettings,
		ss.DB, ss.Agents, ss.EventCenter,
		ss.Pullers, ss.ReviewDispatcher, ss.MetricsCollector)
	if err != nil {
		ss.Pullers.HAStatusPuller.Shutdown()
		ss.Pullers.KeaHostsPuller.Shutdown()
		ss.Pullers.KeaStatsPuller.Shutdown()
		ss.Pullers.Bind9StatsPuller.Shutdown()
		ss.Pullers.AppsStatePuller.Shutdown()
		if ss.MetricsCollector != nil {
			ss.MetricsCollector.Shutdown()
		}

		ss.DB.Close()
		return nil, err
	}
	ss.RestAPI = r

	ss.EventCenter.AddInfoEvent("started Stork Server", "version: "+stork.Version+"\nbuild date: "+stork.BuildDate)

	return ss, nil
}

// Run Stork Server.
func (ss *StorkServer) Serve() {
	// Start listening for requests from ReST API.
	err := ss.RestAPI.Serve()
	if err != nil {
		log.Fatalf("FATAL error: %+v", err)
	}
}

// Shutdown for Stork Server state.
func (ss *StorkServer) Shutdown() {
	ss.EventCenter.AddInfoEvent("shutting down Stork Server")
	log.Println("Shutting down Stork Server")
	ss.RestAPI.Shutdown()
	ss.Pullers.HAStatusPuller.Shutdown()
	ss.Pullers.KeaHostsPuller.Shutdown()
	ss.Pullers.KeaStatsPuller.Shutdown()
	ss.Pullers.Bind9StatsPuller.Shutdown()
	ss.Pullers.AppsStatePuller.Shutdown()
	ss.Agents.Shutdown()
	ss.EventCenter.Shutdown()
	ss.ReviewDispatcher.Shutdown()
	if ss.MetricsCollector != nil {
		ss.MetricsCollector.Shutdown()
	}
	ss.DB.Close()
	log.Println("Stork Server shut down")
}
