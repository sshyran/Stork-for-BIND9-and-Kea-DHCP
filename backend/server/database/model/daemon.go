package dbmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-pg/pg/v10"
	pkgerrors "github.com/pkg/errors"
	keaconfig "isc.org/stork/appcfg/kea"
	dbops "isc.org/stork/server/database"
	storkutil "isc.org/stork/util"
)

const (
	DaemonNameBind9  = "named"
	DaemonNameDHCPv4 = "dhcp4"
	DaemonNameDHCPv6 = "dhcp6"
)

// KEA

// A structure reflecting Kea DHCP stats for daemon. It is stored
// as a JSONB value in SQL and unmarshaled in this structure.
type KeaDHCPDaemonStats struct {
	RPS1            int `pg:"rps1"`
	RPS2            int `pg:"rps2"`
	AddrUtilization int16
	PdUtilization   int16
}

// A structure holding Kea DHCP specific information about a daemon. It
// reflects the kea_dhcp_daemon table which extends the daemon and
// kea_daemon tables with the Kea DHCPv4 or DHCPv6 specific information.
type KeaDHCPDaemon struct {
	tableName   struct{} `pg:"kea_dhcp_daemon"` //nolint:unused,structcheck
	ID          int64
	KeaDaemonID int64
	Stats       KeaDHCPDaemonStats

	// Optionally initialized structure holding indexed collection
	// of subnets. It is created from the KeaDaemon.Config field.
	IndexedSubnets *keaconfig.IndexedSubnets `pg:"-"`
}

// A structure holding common information for all Kea daemons. It
// reflects the information stored in the kea_daemon table.
type KeaDaemon struct {
	ID         int64
	Config     *KeaConfig `pg:",use_zero"`
	ConfigHash string
	DaemonID   int64

	KeaDHCPDaemon *KeaDHCPDaemon `pg:"rel:belongs-to"`
}

// BIND 9

// A structure holding named zone statistics.
type Bind9StatsZone struct {
	Name     string
	Class    string
	Serial   uint32
	ZoneType string
}

// A structure holding named resolver statistics.
type Bind9StatsResolver struct {
	Stats      map[string]int64
	Qtypes     map[string]int64
	Cache      map[string]int64
	CacheStats map[string]int64
	Adb        map[string]int64
}

// A structure holding named view statistics.
type Bind9StatsView struct {
	Zones    []*Bind9StatsZone
	Resolver *Bind9StatsResolver
}

// A structure holding named socket statistics.
type Bind9StatsSocket struct {
	ID           string
	References   int64
	SocketType   string
	PeerAddress  string
	LocalAddress string
	States       []string
}

// A structure holding named socket manager statistics.
type Bind9StatsSocketMgr struct {
	Sockets []*Bind9StatsSocket
}

// A structure holding named task statistics.
type Bind9StatsTask struct {
	ID         string
	Name       string
	References int64
	State      string
	Quantum    int64
	Events     int64
}

// A structure holding named task manager statistics.
type Bind9StatsTaskMgr struct {
	ThreadModel    string
	WorkerThreads  int64
	DefaultQuantum int64
	TasksRunning   int64
	TasksReady     int64
	Tasks          []*Bind9StatsTask
}

// A structure holding named context statistics.
type Bind9StatsContext struct {
	ID         string
	Name       string
	References int64
	Total      int64
	InUse      int64
	MaxInUse   int64
	BlockSize  int64
	Pools      int64
	HiWater    int64
	LoWater    int64
}

// A structure holding named memory statistics.
type Bind9StatsMemory struct {
	TotalUse    int64
	InUse       int64
	BlockSize   int64
	ContextSize int64
	Lost        int64
	Contexts    []*Bind9StatsContext
}

// A structure holding named traffic statistics.
type Bind9StatsTraffic struct {
	SizeBucket map[string]int64
}

// A structure holding named statistics.
type Bind9NamedStats struct {
	JSONStatsVersion string
	BootTime         string
	ConfigTime       string
	CurrentTime      string
	NamedVersion     string
	OpCodes          map[string]int64
	Rcodes           map[string]int64
	Qtypes           map[string]int64
	NsStats          map[string]int64
	Views            map[string]*Bind9StatsView
	SockStats        map[string]int64
	SocketMgr        *Bind9StatsSocketMgr
	TaskMgr          *Bind9StatsTaskMgr
	Memory           *Bind9StatsMemory
	Traffic          map[string]*Bind9StatsTraffic
}

// A structure reflecting BIND 9 stats for a daemon. It is stored as a JSONB
// value in SQL and unmarshaled to this structure.
type Bind9DaemonStats struct {
	ZoneCount          int64
	AutomaticZoneCount int64
	NamedStats         *Bind9NamedStats
}

// A structure holding BIND9 daemon specific information.
type Bind9Daemon struct {
	ID       int64
	DaemonID int64
	Stats    Bind9DaemonStats
}

// A structure reflecting all SQL tables holding information about the
// daemons of various types. It embeds the KeaDaemon structure which
// holds Kea DHCP specific information for Kea daemons. It is nil
// if the daemon is not of the Kea type. Similarly, it holds BIND9
// specific information in the Bind9Daemon structure if the daemon
// type is BIND9. The daemon structure is to be extended with additional
// embedded structures as more daemon types are defined.
type Daemon struct {
	ID              int64
	Pid             int32
	Name            string
	Active          bool `pg:",use_zero"`
	Monitored       bool `pg:",use_zero"`
	Version         string
	ExtendedVersion string
	Uptime          int64
	CreatedAt       time.Time
	ReloadedAt      time.Time

	AppID int64
	App   *App `pg:"rel:has-one"`

	Services []*Service `pg:"many2many:daemon_to_service,fk:daemon_id,join_fk:service_id"`

	LogTargets []*LogTarget `pg:"rel:has-many"`

	KeaDaemon   *KeaDaemon   `pg:"rel:belongs-to"`
	Bind9Daemon *Bind9Daemon `pg:"rel:belongs-to"`

	ConfigReview *ConfigReview `pg:"rel:belongs-to"`
}

// Structure representing HA service information displayed for the daemon
// in the dashboard.
type DaemonServiceOverview struct {
	State         string
	LastFailureAt time.Time
}

// Creates an instance of a Kea daemon. If the daemon name is dhcp4 or
// dhcp6, the instance of the KeaDHCPDaemon is also created.
func NewKeaDaemon(name string, active bool) *Daemon {
	daemon := &Daemon{
		Name:      name,
		Active:    active,
		Monitored: true,
		KeaDaemon: &KeaDaemon{},
	}
	if name == DaemonNameDHCPv4 || name == DaemonNameDHCPv6 {
		daemon.KeaDaemon.KeaDHCPDaemon = &KeaDHCPDaemon{}
	}
	return daemon
}

// Creates an instance of the Bind9 daemon.
func NewBind9Daemon(active bool) *Daemon {
	daemon := &Daemon{
		Name:        DaemonNameBind9,
		Active:      active,
		Monitored:   true,
		Bind9Daemon: &Bind9Daemon{},
	}
	return daemon
}

// Get daemon by ID.
func GetDaemonByID(db *pg.DB, id int64) (*Daemon, error) {
	app := Daemon{}
	q := db.Model(&app)
	q = q.Relation("App")
	q = q.Relation("App.Machine")
	q = q.Relation("KeaDaemon")
	q = q.Where("daemon.id = ?", id)
	err := q.Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, pkgerrors.Wrapf(err, "problem with getting daemon %v", id)
	}
	return &app, nil
}

// Select one or more daemons for update. The main use case for this function is
// to prevent modifications and deletions of the daemons while the server inserts
// config reports for them. It must be called within a transaction and the selected
// rows remain locked until the transaction is committed or rolled back. Due to the
// limitations of the go-pg library, this function does not select all columns in the
// joined tables (machine and app).
func GetDaemonsForUpdate(tx *pg.Tx, daemonsToSelect []*Daemon) ([]*Daemon, error) {
	var daemons []*Daemon

	// It is an error when no daemons are specified. Typically it will be one
	// daemon but there can be more.
	if len(daemonsToSelect) == 0 {
		return daemons, pkgerrors.New("no daemons specified for select for update")
	}

	// Execute SELECT ... FROM daemon ... FOR UPDATE. It locks all selected
	// rows of the daemon table and the selected rows of the joined tables.
	// PostgreSQL does not allow for such locking when LEFT JOIN is used
	// because some joined rows may be NULL in this case. Unfortunately,
	// the go-pg Relation() does not allow for choosing between the INNER and
	// LEFT JOIN. Therefore, we can't use the Relation() call in this query.
	// Instead, we use explicit Join() and ColumnExpr() calls. It requires
	// explicitly specifying the selected column names. Thus, we limited the
	// selected columns to ID for joined tables to avoid having to maintain
	// the selected columns list. The query can be extended to select more
	// columns if necessary.
	var ids []int64
	for _, d := range daemonsToSelect {
		ids = append(ids, d.ID)
	}
	err := tx.Model(&daemons).
		ColumnExpr("daemon.*").
		ColumnExpr("app.id AS app__id").
		ColumnExpr("machine.id AS app__machine__id").
		Join("INNER JOIN app ON app.id = daemon.app_id").
		Join("INNER JOIN machine ON app.machine_id = machine.id").
		Where("daemon.id IN (?)", pg.In(ids)).
		For("UPDATE").
		OrderExpr("daemon.id ASC").
		Select()

	if errors.Is(err, pg.ErrNoRows) {
		return daemons, nil
	} else if err != nil {
		var sids []string
		for _, id := range ids {
			sids = append(sids, fmt.Sprintf("%d", id))
		}
		return nil, pkgerrors.Wrapf(err, "problem with selecting daemons with ids: %s, for update",
			strings.Join(sids, ", "))
	}
	return daemons, nil
}

// Select one or more Kea daemons for update. The main use case for this function is
// to prevent modifications and deletions of the Kea daemons while the server inserts
// config reports for them. It must be called within a transaction and the selected
// rows remain locked until the transaction is committed or rolled back. Due to the
// limitations of the go-pg library, this function does not select all columns in the
// joined tables (machine, app and kea_daemon). It selects the config and the
// config_hash columns from the kea_daemon so the configreview implementation can
// verify that the configurations were not changed during the config review process.
// For other joined tables, this function merely returns id columns values.
func GetKeaDaemonsForUpdate(tx *pg.Tx, daemonsToSelect []*Daemon) ([]*Daemon, error) {
	var daemons []*Daemon

	// It is an error when no daemons are specified. Typically it will be one
	// daemon but there can be more.
	if len(daemonsToSelect) == 0 {
		return daemons, pkgerrors.New("no Kea daemons specified for select for update")
	}

	// Execute SELECT ... FROM daemon ... FOR UPDATE. It locks all selected
	// rows of the daemon table and the selected rows of the joined tables.
	// PostgreSQL does not allow for such locking when LEFT JOIN is used
	// because some joined rows may be NULL in this case. Unfortunately,
	// the go-pg Relation() does not allow for choosing between the INNER and
	// LEFT JOIN. Therefore, we can't use the Relation() call in this query.
	// Instead, we use explicit Join() and ColumnExpr() calls. It requires
	// explicitly specifying the selected column names. Thus, we limited the
	// selected columns to ID for joined tables to avoid having to maintain
	// the selected columns list. The query can be extended to select more
	// columns if necessary.
	var ids []int64
	for _, d := range daemonsToSelect {
		ids = append(ids, d.ID)
	}
	err := tx.Model(&daemons).
		ColumnExpr("daemon.*").
		ColumnExpr("kea_daemon.id AS kea_daemon__id, kea_daemon.config AS kea_daemon__config, kea_daemon.config_hash AS kea_daemon__config_hash").
		ColumnExpr("app.id AS app__id").
		ColumnExpr("machine.id AS app__machine__id").
		Join("INNER JOIN kea_daemon ON kea_daemon.daemon_id = daemon.id").
		Join("INNER JOIN app ON app.id = daemon.app_id").
		Join("INNER JOIN machine ON app.machine_id = machine.id").
		Where("daemon.id IN (?)", pg.In(ids)).
		For("UPDATE").
		OrderExpr("daemon.id ASC").
		Select()

	if errors.Is(err, pg.ErrNoRows) {
		return daemons, nil
	} else if err != nil {
		var sids []string
		for _, id := range ids {
			sids = append(sids, fmt.Sprintf("%d", id))
		}
		return nil, pkgerrors.Wrapf(err, "problem with selecting Kea daemons with ids: %s, for update",
			strings.Join(sids, ", "))
	}
	return daemons, nil
}

// Updates a daemon in a transaction, including dependent Daemon,
// KeaDaemon, KeaDHCPDaemon and Bind9Daemon if they are not nil.
func updateDaemon(tx *pg.Tx, daemon *Daemon) error {
	// Update common daemon instance.
	result, err := tx.Model(daemon).WherePK().Update()
	if err != nil {
		return pkgerrors.Wrapf(err, "problem with updating daemon %d", daemon.ID)
	} else if result.RowsAffected() <= 0 {
		return pkgerrors.Wrapf(ErrNotExists, "daemon with id %d does not exist", daemon.ID)
	}

	// If this is a Kea daemon, we have to update Kea specific tables too.
	if daemon.KeaDaemon != nil && daemon.KeaDaemon.ID != 0 {
		// Make sure that the KeaDaemon points to the Daemon.
		daemon.KeaDaemon.DaemonID = daemon.ID
		result, err := tx.Model(daemon.KeaDaemon).WherePK().Update()
		if err != nil {
			return pkgerrors.Wrapf(err, "problem with updating general Kea specific information for daemon %d",
				daemon.ID)
		} else if result.RowsAffected() <= 0 {
			return pkgerrors.Wrapf(ErrNotExists, "Kea daemon with id %d does not exist", daemon.KeaDaemon.ID)
		}

		// If this is Kea DHCP daemon, there is one more table to update.
		if daemon.KeaDaemon.KeaDHCPDaemon != nil && daemon.KeaDaemon.KeaDHCPDaemon.ID != 0 {
			daemon.KeaDaemon.KeaDHCPDaemon.KeaDaemonID = daemon.KeaDaemon.ID
			result, err := tx.Model(daemon.KeaDaemon.KeaDHCPDaemon).WherePK().Update()
			if err != nil {
				return pkgerrors.Wrapf(err, "problem with updating general Kea DHCP information for daemon %d",
					daemon.ID)
			} else if result.RowsAffected() <= 0 {
				return pkgerrors.Wrapf(ErrNotExists, "Kea DHCP daemon with id %d does not exist",
					daemon.KeaDaemon.KeaDHCPDaemon.ID)
			}
		}
	} else if daemon.Bind9Daemon != nil && daemon.Bind9Daemon.ID != 0 {
		// This is Bind9 daemon. Update the Bind9 specific table.
		daemon.Bind9Daemon.DaemonID = daemon.ID
		result, err := tx.Model(daemon.Bind9Daemon).WherePK().Update()
		if err != nil {
			return pkgerrors.Wrapf(err, "problem with updating BIND9 specific information for daemon %d",
				daemon.ID)
		} else if result.RowsAffected() <= 0 {
			return pkgerrors.Wrapf(ErrNotExists, "BIND9 daemon with id %d does not exist", daemon.Bind9Daemon.ID)
		}
	}
	return nil
}

// Updates a daemon, including dependent Daemon, KeaDaemon, KeaDHCPDaemon
// and Bind9Daemon if they are not nil.
func UpdateDaemon(dbi dbops.DBI, daemon *Daemon) error {
	if db, ok := dbi.(*pg.DB); ok {
		return db.RunInTransaction(context.Background(), func(tx *pg.Tx) error {
			return updateDaemon(tx, daemon)
		})
	}
	return updateDaemon(dbi.(*pg.Tx), daemon)
}

// This is a hook to go-pg that is called just after reading rows from database.
// It reconverts KeaDaemon's configuration from json string maps to the
// expected structure in GO.
func (d *KeaDaemon) AfterScan(ctx context.Context) error {
	if d.Config == nil {
		return nil
	}

	bytes, err := json.Marshal(d.Config)
	if err != nil {
		return pkgerrors.Wrapf(err, "problem with marshalling Kea config: %+v ", *d.Config)
	}

	err = json.Unmarshal(bytes, d.Config)
	if err != nil {
		return pkgerrors.Wrapf(err, "problem with unmarshalling Kea config")
	}
	return nil
}

// Returns a slice containing HA information specific for the daemon. This function
// assumes that the daemon has been fetched from the database along with the
// services. It doesn't perform database queries on its own.
func (d *Daemon) GetHAOverview() (overviews []DaemonServiceOverview) {
	for _, service := range d.Services {
		if service.HAService == nil {
			continue
		}
		var overview DaemonServiceOverview
		overview.State = service.GetDaemonHAState(d.ID)
		overview.LastFailureAt = service.GetPartnerHAFailureTime(d.ID)
		overviews = append(overviews, overview)
	}
	return overviews
}

// Sets new configuration of the daemon. This function should be used to set
// new daemon configuration instead of simple configuration assignment because
// it extracts some configuration information and populates to the daemon structures,
// e.g. logging configuration. The config should be a pointer to the KeaConfig
// structure. The config_hash is a hash created from the specified configuration.
func (d *Daemon) SetConfigWithHash(config interface{}, configHash string) error {
	if d.KeaDaemon != nil {
		parsedConfig, ok := config.(*KeaConfig)
		if !ok {
			return pkgerrors.Errorf("error setting non Kea config for Kea daemon %s", d.Name)
		}

		existingLogTargets := d.LogTargets
		d.LogTargets = []*LogTarget{}
		loggers := parsedConfig.GetLoggers()
		for _, logger := range loggers {
			targets := NewLogTargetsFromKea(logger)
			for i := range targets {
				// For each target check if it already exists and inherit its
				// ID and creation time.
				for _, existingTarget := range existingLogTargets {
					if targets[i].Name == existingTarget.Name &&
						targets[i].Output == existingTarget.Output &&
						existingTarget.DaemonID == d.ID {
						targets[i].ID = existingTarget.ID
						targets[i].DaemonID = d.ID
						targets[i].CreatedAt = existingTarget.CreatedAt
					}
				}
				d.LogTargets = append(d.LogTargets, targets[i])
			}
		}
		d.KeaDaemon.Config = parsedConfig
		d.KeaDaemon.ConfigHash = configHash
	}
	return nil
}

// Sets new configuration of the daemon with empty hash.
func (d *Daemon) SetConfig(config interface{}) error {
	return d.SetConfigWithHash(config, "")
}

// Sets new configuration specified as JSON string. Internally, it calls
// SetConfig after parsing the JSON configuration.
func (d *Daemon) SetConfigFromJSON(config string) error {
	if d.KeaDaemon != nil {
		parsedConfig, err := keaconfig.NewFromJSON(config)
		if err != nil {
			return err
		}
		return d.SetConfigWithHash(parsedConfig, storkutil.Fnv128(config))
	}
	return nil
}

// Returns local subnet ID for a given subnet prefix. If subnets have been indexed,
// this function will use the index to find a subnet with the matching prefix. This
// is much faster, but requires that the caller first builds a collection of
// indexed subnets and associates it with appropriate KeaDHCPDaemon instances.
// If the indexes are not built, this function will simply iterate over the
// subnets within the configuration. This is generally much slower and should
// be only used for sporadic calls to GetLocalSubnetID().
// If the matching subnet is not found, the 0 value is returned.
func (d *Daemon) GetLocalSubnetID(prefix string) int64 {
	if d.KeaDaemon == nil {
		return 0
	}
	// If subnets happen to be indexed, use the index to locate the subnet because
	// it is a lot faster than full scan.
	if d.KeaDaemon.KeaDHCPDaemon != nil && d.KeaDaemon.KeaDHCPDaemon.IndexedSubnets != nil {
		is := d.KeaDaemon.KeaDHCPDaemon.IndexedSubnets
		if subnet, ok := is.ByPrefix[prefix]; ok {
			if id, ok := subnet["id"].(float64); ok {
				return int64(id)
			}
		}

		return 0
	}

	// No index for subnets for this daemon. We have to do full subnets scan.
	if d.KeaDaemon.Config != nil {
		if id := d.KeaDaemon.Config.GetLocalSubnetID(prefix); id > 0 {
			return id
		}
	}
	return 0
}

// Creates shallow copy of KeaDaemon, i.e. copies Daemon structure and
// nested KeaDaemon structure. The new instance of KeaDaemon is created
// but the pointers under KeaDaemon are inherited from the source.
func ShallowCopyKeaDaemon(daemon *Daemon) *Daemon {
	copied := &Daemon{}
	*copied = *daemon
	if daemon.KeaDaemon != nil {
		copied.KeaDaemon = &KeaDaemon{}
		*copied.KeaDaemon = *daemon.KeaDaemon
	}
	return copied
}
