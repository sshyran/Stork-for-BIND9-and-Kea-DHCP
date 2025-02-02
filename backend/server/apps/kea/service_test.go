package kea

import (
	"encoding/json"
	"fmt"
	"testing"

	require "github.com/stretchr/testify/require"
	dbmodel "isc.org/stork/server/database/model"
	dbtest "isc.org/stork/server/database/test"
)

// Returns DHCP server configuration created from a template. The template
// parameters include root parameter, i.e. Dhcp4 or Dhcp6, High Availability
// mode and a variadic list of HA peers. The peers are identified by names:
// server1, server2 ...  server5. The server1 is a primary, the server2
// is a secondary, the server3 is a standby and the remaining ones are the
// backup servers.
func getHATestConfig(rootName, thisServerName, mode string, peerNames ...string) *dbmodel.KeaConfig {
	type peerInfo struct {
		URL  string
		Role string
	}
	// Map server names to peer configurations.
	peers := map[string]peerInfo{
		"server1": {
			URL:  "http://192.0.2.33:8000",
			Role: "primary",
		},
		"server2": {
			URL:  "http://192.0.2.66:8000",
			Role: "secondary",
		},
		"server3": {
			URL:  "http://192.0.2.66:8000",
			Role: "standby",
		},
		"server4": {
			URL:  "http://192.0.2.133:8000",
			Role: "backup",
		},
		"server5": {
			URL:  "http://192.0.2.166:8000",
			Role: "backup",
		},
	}

	// Output configuration of the peers from the template.
	var peersList string
	for _, peerName := range peerNames {
		if peer, ok := peers[peerName]; ok {
			peerTemplate := `
                {
                    "name": "%s",
                    "url":  "%s",
                    "role": "%s"
                }`
			peerTemplate = fmt.Sprintf(peerTemplate, peerName, peer.URL, peer.Role)
			if len(peersList) > 0 {
				peersList += ",\n"
			}
			peersList += peerTemplate
		}
	}

	// Output the server configuration from the template.
	configStr := `{
        "%s": {
            "hooks-libraries": [
                {
                    "library": "libdhcp_ha.so",
                    "parameters": {
                        "high-availability": [{
                            "this-server-name": "%s",
                            "mode": "%s",
                            "peers": [ %s ]
                        }]
                    }
                }
            ]
        }
    }`
	configStr = fmt.Sprintf(configStr, rootName, thisServerName, mode, peersList)

	// Convert the configuration from JSON to KeaConfig.
	var config dbmodel.KeaConfig
	_ = json.Unmarshal([]byte(configStr), &config)
	return &config
}

// Multi step test verifying that services can be gradually created from the
// Kea apps being added to the database.
func TestDetectHAServices(t *testing.T) {
	db, _, teardown := dbtest.SetupDatabaseTestCase(t)
	defer teardown()

	// Add the first machine.
	m := &dbmodel.Machine{
		ID:        0,
		Address:   "localhost",
		AgentPort: 8080,
	}
	err := dbmodel.AddMachine(db, m)
	require.NoError(t, err)

	// Add the first Kea being a DHCPv4 secondary in load-balancing configuration
	// and the DHCPv6 standby in the standby configuration.
	var accessPoints []*dbmodel.AccessPoint
	accessPoints = dbmodel.AppendAccessPoint(accessPoints, dbmodel.AccessPointControl, "192.0.2.66", "", 8000, false)

	app := dbmodel.App{
		MachineID:    m.ID,
		Type:         dbmodel.AppTypeKea,
		AccessPoints: accessPoints,
		Daemons: []*dbmodel.Daemon{
			{
				Name: "dhcp4",
				KeaDaemon: &dbmodel.KeaDaemon{
					Config: getHATestConfig("Dhcp4", "server2", "load-balancing",
						"server1", "server2", "server4"),
					KeaDHCPDaemon: &dbmodel.KeaDHCPDaemon{},
				},
			},
			{
				Name: "dhcp6",
				KeaDaemon: &dbmodel.KeaDaemon{
					Config: getHATestConfig("Dhcp6", "server3", "hot-standby",
						"server1", "server3"),
					KeaDHCPDaemon: &dbmodel.KeaDHCPDaemon{},
				},
			},
		},
	}

	// Add the app to the database so as it gets its ID.
	_, err = dbmodel.AddApp(db, &app)
	require.NoError(t, err)
	require.NotZero(t, app.ID)

	// Run service detection for all daemons in this app.
	var services []dbmodel.Service
	for i := range app.Daemons {
		services = append(services, DetectHAServices(db, app.Daemons[i])...)
	}

	// There should be two services returned, one for DHCPv4 and one for DHCPv6.
	require.Len(t, services, 2)

	// Check the DHCPv4 service first.
	require.True(t, services[0].IsNew())
	require.NotNil(t, services[0].HAService)
	require.Equal(t, "dhcp4", services[0].HAService.HAType)
	require.Equal(t, "load-balancing", services[0].HAService.HAMode)
	require.Zero(t, services[0].HAService.PrimaryID)
	require.Equal(t, app.Daemons[0].ID, services[0].HAService.SecondaryID)
	require.Empty(t, services[0].HAService.BackupID)
	require.True(t, services[0].HAService.PrimaryStatusCollectedAt.IsZero())
	require.True(t, services[0].HAService.SecondaryStatusCollectedAt.IsZero())
	require.Empty(t, services[0].HAService.PrimaryLastState)
	require.Empty(t, services[0].HAService.SecondaryLastState)

	// Check the DHCPv6 service.
	require.True(t, services[1].IsNew())
	require.NotNil(t, services[1].HAService)
	require.Equal(t, "dhcp6", services[1].HAService.HAType)
	require.Equal(t, "hot-standby", services[1].HAService.HAMode)
	require.Zero(t, services[1].HAService.PrimaryID)
	require.Equal(t, app.Daemons[1].ID, services[1].HAService.SecondaryID)
	require.Empty(t, services[1].HAService.BackupID)
	require.True(t, services[1].HAService.PrimaryStatusCollectedAt.IsZero())
	require.True(t, services[1].HAService.SecondaryStatusCollectedAt.IsZero())
	require.Empty(t, services[1].HAService.PrimaryLastState)
	require.Empty(t, services[1].HAService.SecondaryLastState)

	// These are new services, so the daemons should have been added to them.
	require.Len(t, services[0].Daemons, 1)
	require.Len(t, services[1].Daemons, 1)

	// Add the services to the database.
	err = dbmodel.AddService(db, &services[0])
	require.NoError(t, err)
	err = dbmodel.AddService(db, &services[1])
	require.NoError(t, err)

	// Run service detection again. The existing services should be returned.
	services = []dbmodel.Service{}
	for i := range app.Daemons {
		services = append(services, DetectHAServices(db, app.Daemons[i])...)
	}
	require.Len(t, services, 2)

	// This is no longer a new service.
	require.False(t, services[0].IsNew())
	require.Equal(t, "dhcp4", services[0].HAService.HAType)
	require.Equal(t, "load-balancing", services[0].HAService.HAMode)
	require.Zero(t, services[0].HAService.PrimaryID)
	require.Equal(t, app.Daemons[0].ID, services[0].HAService.SecondaryID)
	require.Empty(t, services[0].HAService.BackupID)

	require.False(t, services[1].IsNew())
	require.Equal(t, "dhcp6", services[1].HAService.HAType)
	require.Equal(t, "hot-standby", services[1].HAService.HAMode)
	require.Zero(t, services[1].HAService.PrimaryID)
	require.Equal(t, app.Daemons[1].ID, services[1].HAService.SecondaryID)
	require.Empty(t, services[1].HAService.BackupID)

	// The daemons should be already associated with the services.
	require.Len(t, services[0].Daemons, 1)
	require.Len(t, services[1].Daemons, 1)

	// Add machine and app for the DHCPv4 backup server.
	m = &dbmodel.Machine{
		ID:        0,
		Address:   "backup1",
		AgentPort: 8080,
	}
	err = dbmodel.AddMachine(db, m)
	require.NoError(t, err)

	accessPoints = []*dbmodel.AccessPoint{}
	accessPoints = dbmodel.AppendAccessPoint(accessPoints, dbmodel.AccessPointControl, "192.0.2.133", "", 8000, false)
	app = dbmodel.App{
		MachineID:    m.ID,
		Type:         dbmodel.AppTypeKea,
		AccessPoints: accessPoints,
		Daemons: []*dbmodel.Daemon{
			{
				Name: "dhcp4",
				KeaDaemon: &dbmodel.KeaDaemon{
					Config: getHATestConfig("Dhcp4", "server4", "load-balancing",
						"server1", "server2", "server4"),
					KeaDHCPDaemon: &dbmodel.KeaDHCPDaemon{},
				},
			},
		},
	}
	_, err = dbmodel.AddApp(db, &app)
	require.NoError(t, err)
	require.NotZero(t, app.ID)

	// This time, we've added the server with only a DHCPv4 configuration.
	// Therefore, only a DHCPv4 service should be returned for this app.
	services = DetectHAServices(db, app.Daemons[0])
	require.Len(t, services, 1)
	require.False(t, services[0].IsNew())
	require.NotNil(t, services[0].HAService)
	require.Equal(t, "dhcp4", services[0].HAService.HAType)
	require.Equal(t, "load-balancing", services[0].HAService.HAMode)
	require.Zero(t, services[0].HAService.PrimaryID)
	require.NotZero(t, services[0].HAService.SecondaryID)
	require.Len(t, services[0].HAService.BackupID, 1)
	require.Equal(t, app.Daemons[0].ID, services[0].HAService.BackupID[0])

	require.Len(t, services[0].Daemons, 1)

	// We have to update the service in the database according to the value
	// returned by the detection routine.
	err = dbmodel.UpdateBaseHAService(db, services[0].HAService)
	require.NoError(t, err)

	// We also have to associate the daemon with the service.
	err = dbmodel.AddDaemonToService(db, services[0].ID, app.Daemons[0])
	require.NoError(t, err)

	// Add machine and app for the primary server.
	m = &dbmodel.Machine{
		ID:        0,
		Address:   "primary",
		AgentPort: 8080,
	}
	err = dbmodel.AddMachine(db, m)
	require.NoError(t, err)

	// The primary server includes both DHCPv4 and DHCPv6 configurations.
	accessPoints = []*dbmodel.AccessPoint{}
	accessPoints = dbmodel.AppendAccessPoint(accessPoints, dbmodel.AccessPointControl, "192.0.2.33", "", 8000, true)
	app = dbmodel.App{
		MachineID:    m.ID,
		Type:         dbmodel.AppTypeKea,
		AccessPoints: accessPoints,
		Daemons: []*dbmodel.Daemon{
			{
				Name: "dhcp4",
				KeaDaemon: &dbmodel.KeaDaemon{
					Config: getHATestConfig("Dhcp4", "server1", "load-balancing",
						"server1", "server2", "server4"),
					KeaDHCPDaemon: &dbmodel.KeaDHCPDaemon{},
				},
			},
			{
				Name: "dhcp6",
				KeaDaemon: &dbmodel.KeaDaemon{
					Config: getHATestConfig("Dhcp6", "server1", "hot-standby",
						"server1", "server3"),
					KeaDHCPDaemon: &dbmodel.KeaDHCPDaemon{},
				},
			},
		},
	}
	_, err = dbmodel.AddApp(db, &app)
	require.NoError(t, err)
	require.NotZero(t, app.ID)

	// Since we have added two HA configurations for this app, there should
	// be two services returned, one for DHCPv4 and one for DHCPv6.
	services = []dbmodel.Service{}
	for i := range app.Daemons {
		services = append(services, DetectHAServices(db, app.Daemons[i])...)
	}
	require.Len(t, services, 2)
	require.False(t, services[0].IsNew())
	require.NotNil(t, services[0].HAService)
	require.Equal(t, "dhcp4", services[0].HAService.HAType)
	require.Equal(t, "load-balancing", services[0].HAService.HAMode)
	require.Equal(t, app.Daemons[0].ID, services[0].HAService.PrimaryID)
	require.NotZero(t, services[0].HAService.SecondaryID)
	require.Len(t, services[0].HAService.BackupID, 1)

	require.False(t, services[1].IsNew())
	require.NotNil(t, services[1].HAService)
	require.Equal(t, "dhcp6", services[1].HAService.HAType)
	require.Equal(t, "hot-standby", services[1].HAService.HAMode)
	require.Equal(t, app.Daemons[1].ID, services[1].HAService.PrimaryID)
	require.NotZero(t, services[1].HAService.SecondaryID)
	require.Empty(t, services[1].HAService.BackupID)

	// The DHCPv4 service should have two daemons associated with it, one for
	// the secondary server and one for the backup. We will have to associate
	// the primary server with this service on our own.
	require.Len(t, services[0].Daemons, 2)

	// The DHCPv6 service should have only one daemon associated, i.e. the
	// secondary server, because we didn't add the backup server to it.
	require.Len(t, services[1].Daemons, 1)

	// Update the services in the database.
	err = dbmodel.UpdateBaseHAService(db, services[0].HAService)
	require.NoError(t, err)
	err = dbmodel.AddDaemonToService(db, services[0].ID, app.Daemons[0])
	require.NoError(t, err)

	err = dbmodel.UpdateBaseHAService(db, services[1].HAService)
	require.NoError(t, err)
	err = dbmodel.AddDaemonToService(db, services[1].ID, app.Daemons[1])
	require.NoError(t, err)

	// Add machine and app for another DHCPv4 backup server.
	m = &dbmodel.Machine{
		ID:        0,
		Address:   "backup2",
		AgentPort: 8080,
	}
	err = dbmodel.AddMachine(db, m)
	require.NoError(t, err)

	accessPoints = []*dbmodel.AccessPoint{}
	accessPoints = dbmodel.AppendAccessPoint(accessPoints, dbmodel.AccessPointControl, "192.0.2.166", "", 8000, false)
	app = dbmodel.App{
		MachineID:    m.ID,
		Type:         dbmodel.AppTypeKea,
		AccessPoints: accessPoints,
		Daemons: []*dbmodel.Daemon{
			{
				Name: "dhcp4",
				KeaDaemon: &dbmodel.KeaDaemon{
					Config: getHATestConfig("Dhcp4", "server5", "load-balancing",
						"server1", "server2", "server4", "server5"),
					KeaDHCPDaemon: &dbmodel.KeaDHCPDaemon{},
				},
			},
		},
	}
	_, err = dbmodel.AddApp(db, &app)
	require.NoError(t, err)
	require.NotZero(t, app.ID)

	services = []dbmodel.Service{}
	for i := range app.Daemons {
		services = append(services, DetectHAServices(db, app.Daemons[i])...)
	}
	require.Len(t, services, 1)
	require.False(t, services[0].IsNew())
	require.NotNil(t, services[0].HAService)
	require.Equal(t, "dhcp4", services[0].HAService.HAType)
	require.Equal(t, "load-balancing", services[0].HAService.HAMode)
	require.NotZero(t, services[0].HAService.PrimaryID)
	require.NotZero(t, services[0].HAService.SecondaryID)
	require.Len(t, services[0].HAService.BackupID, 2)
	require.Contains(t, services[0].HAService.BackupID, app.Daemons[0].ID)

	require.Len(t, services[0].Daemons, 3)
}

// Test that a daemon doesn't belong to a blank service , i.e. a
// service that comprises no daemons.
func TestAppBelongsToHAServiceBlankService(t *testing.T) {
	// Create blank service.
	service := &dbmodel.Service{
		BaseService: dbmodel.BaseService{
			ServiceType: "ha_dhcp",
		},
		HAService: &dbmodel.BaseHAService{
			HAType: "dhcp4",
		},
	}
	// Create an app.
	var accessPoints []*dbmodel.AccessPoint
	accessPoints = dbmodel.AppendAccessPoint(accessPoints, dbmodel.AccessPointControl, "192.0.2.66", "", 8000, true)
	app := &dbmodel.App{
		Type:         dbmodel.AppTypeKea,
		AccessPoints: accessPoints,
		Daemons: []*dbmodel.Daemon{
			{
				Name: "dhcp4",
				KeaDaemon: &dbmodel.KeaDaemon{
					Config: getHATestConfig("Dhcp4", "server2", "load-balancing",
						"server1", "server2", "server4"),
					KeaDHCPDaemon: &dbmodel.KeaDHCPDaemon{},
				},
			},
		},
	}

	// The daemon doesn't belong to the service because the service includes
	// no meaningful information to make such determination. In that case
	// it is up to the administrator to explicitly add the daemon to the service.
	require.False(t, daemonBelongsToHAService(app.Daemons[0], service))
}
