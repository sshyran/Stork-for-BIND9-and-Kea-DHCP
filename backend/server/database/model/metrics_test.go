package dbmodel

import (
	"testing"

	"github.com/stretchr/testify/require"
	dbtest "isc.org/stork/server/database/test"
)

// Metrics should not crash even if the database is empty.
func TestEmptyDatabaseMetrics(t *testing.T) {
	// Arrange
	db, _, teardown := dbtest.SetupDatabaseTestCase(t)
	defer teardown()

	// Act
	metrics, err := GetCalculatedMetrics(db)

	// Assert
	require.NoError(t, err)
	require.Zero(t, metrics.AuthorizedMachines)
	require.Zero(t, metrics.UnauthorizedMachines)
	require.Zero(t, metrics.UnreachableMachines)
}

// Metrics based on the machines should be properly calculated.
func TestFilledMachineDatabaseMetrics(t *testing.T) {
	// Arrange
	db, _, teardown := dbtest.SetupDatabaseTestCase(t)
	defer teardown()
	_ = AddMachine(db, &Machine{
		Address: "1", AgentPort: 1, Authorized: false,
	})
	_ = AddMachine(db, &Machine{
		Address: "2", AgentPort: 2, Authorized: false,
	})
	_ = AddMachine(db, &Machine{
		Address: "3", AgentPort: 3, Authorized: true,
	})
	_ = AddMachine(db, &Machine{
		Address: "4", AgentPort: 4, Authorized: false,
	})
	_ = AddMachine(db, &Machine{
		Address: "5", AgentPort: 5, Authorized: false, Error: "5",
	})
	_ = AddMachine(db, &Machine{
		Address: "6", AgentPort: 6, Authorized: true, Error: "6",
	})

	// Act
	metrics, err := GetCalculatedMetrics(db)

	// Assert
	require.NoError(t, err)
	require.EqualValues(t, 2, metrics.AuthorizedMachines)
	require.EqualValues(t, 4, metrics.UnauthorizedMachines)
	require.EqualValues(t, 2, metrics.UnreachableMachines)
}

// Metrics per subnet should be properly calculated.
func TestFilledSubnetsDatabaseMetrics(t *testing.T) {
	// Arrange
	db, _, teardown := dbtest.SetupDatabaseTestCase(t)
	defer teardown()
	_ = AddSubnet(db, &Subnet{
		Prefix:          "192.168.0.1/32",
		AddrUtilization: 10,
		PdUtilization:   15,
	})
	_ = AddSubnet(db, &Subnet{
		Prefix:          "192.168.1.1/32",
		AddrUtilization: 20,
		PdUtilization:   25,
	})
	_ = AddSubnet(db, &Subnet{
		Prefix: "192.168.2.1/32",
	})

	// Act
	metrics, err := GetCalculatedMetrics(db)

	// Assert
	require.NoError(t, err)
	require.Len(t, metrics.SubnetMetrics, 3)

	require.EqualValues(t, "192.168.0.1/32", metrics.SubnetMetrics[0].Label)
	require.EqualValues(t, 10, metrics.SubnetMetrics[0].AddrUtilization)
	require.EqualValues(t, 15, metrics.SubnetMetrics[0].PdUtilization)

	require.EqualValues(t, "192.168.1.1/32", metrics.SubnetMetrics[1].Label)
	require.EqualValues(t, 20, metrics.SubnetMetrics[1].AddrUtilization)
	require.EqualValues(t, 25, metrics.SubnetMetrics[1].PdUtilization)

	require.EqualValues(t, "192.168.2.1/32", metrics.SubnetMetrics[2].Label)
	require.Zero(t, metrics.SubnetMetrics[2].AddrUtilization)
	require.Zero(t, metrics.SubnetMetrics[2].PdUtilization)
}

// Metrics per shared network should be properly calculated.
func TestFilledSharedNetworksDatabaseMetrics(t *testing.T) {
	// Arrange
	db, _, teardown := dbtest.SetupDatabaseTestCase(t)
	defer teardown()
	_ = AddSharedNetwork(db, &SharedNetwork{
		Name:            "alice",
		AddrUtilization: 10,
		PdUtilization:   15,
		Family:          4,
	})
	_ = AddSharedNetwork(db, &SharedNetwork{
		Name:            "bob",
		AddrUtilization: 20,
		PdUtilization:   25,
		Family:          4,
	})
	_ = AddSharedNetwork(db, &SharedNetwork{
		Name:   "eva",
		Family: 6,
	})

	// Act
	metrics, err := GetCalculatedMetrics(db)

	// Assert
	require.NoError(t, err)
	require.Len(t, metrics.SharedNetworkMetrics, 3)

	require.EqualValues(t, "alice", metrics.SharedNetworkMetrics[0].Label)
	require.EqualValues(t, 10, metrics.SharedNetworkMetrics[0].AddrUtilization)
	require.EqualValues(t, 15, metrics.SharedNetworkMetrics[0].PdUtilization)

	require.EqualValues(t, "bob", metrics.SharedNetworkMetrics[1].Label)
	require.EqualValues(t, 20, metrics.SharedNetworkMetrics[1].AddrUtilization)
	require.EqualValues(t, 25, metrics.SharedNetworkMetrics[1].PdUtilization)

	require.EqualValues(t, "eva", metrics.SharedNetworkMetrics[2].Label)
	require.Zero(t, metrics.SharedNetworkMetrics[2].AddrUtilization)
	require.Zero(t, metrics.SharedNetworkMetrics[2].PdUtilization)
}
