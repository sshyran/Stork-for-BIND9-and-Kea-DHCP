package server

import (
	log "github.com/sirupsen/logrus"

	"isc.org/stork/server/agentcomm"
	"isc.org/stork/server/restservice"
)

// Global Stork Server state
type StorkServer struct {
	Agents agentcomm.ConnectedAgents
	RestAPI restservice.RestAPI
}

// Init for Stork Server state
func (ss *StorkServer) Init() {
	err := ss.Agents.Init()
	if err != nil {
		log.Fatalf("FATAL error: %+v", err)
	}

	err = ss.RestAPI.Init(&ss.Agents)
	if err != nil {
		log.Fatalf("FATAL error: %+v", err)
	}
}

// Run Stork Server
func (ss *StorkServer) Serve() {

	// Start listening for requests from ReST API.
	err := ss.RestAPI.Serve()
	if err != nil {
		log.Fatalf("FATAL error: %+v", err)
	}
}

// Shutdown for Stork Server state
func (ss *StorkServer) Shutdown() {
	ss.RestAPI.Shutdown()
	ss.Agents.Shutdown()
}
