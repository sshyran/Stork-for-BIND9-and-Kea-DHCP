package dbmodel

import (
	"testing"

	"github.com/stretchr/testify/require"
	dbtest "isc.org/stork/server/database/test"
)

// Check if settings initialization works.
func TestInitializeSettings(t *testing.T) {
	db, _, teardown := dbtest.SetupDatabaseTestCase(t)
	defer teardown()

	var settings []Setting

	// check if settings are empty
	q := db.Model(&settings)
	err := q.Select()
	require.NoError(t, err)
	require.Empty(t, settings)

	// initialize settings
	err = InitializeSettings(db)
	require.NoError(t, err)

	// check if any settings were added to db
	settings = nil
	q = db.Model(&settings)
	err = q.Select()
	require.NoError(t, err)
	require.NotEmpty(t, settings)
	count := len(settings)

	// check if given setting exists in db and has some default value
	val, err := GetSettingInt(db, "bind9_stats_puller_interval")
	require.NoError(t, err)
	require.EqualValues(t, 60, val)

	val, err = GetSettingInt(db, "kea_stats_puller_interval")
	require.NoError(t, err)
	require.EqualValues(t, 60, val)

	val, err = GetSettingInt(db, "kea_hosts_puller_interval")
	require.NoError(t, err)
	require.EqualValues(t, 60, val)

	val, err = GetSettingInt(db, "kea_status_puller_interval")
	require.NoError(t, err)
	require.EqualValues(t, 30, val)

	// change the setting
	err = SetSettingInt(db, "kea_stats_puller_interval", 123)
	require.NoError(t, err)

	// reinitialize settings, nothing should change
	err = InitializeSettings(db)
	require.NoError(t, err)
	require.Len(t, settings, count)

	// this changed setting should not be reset
	val, err = GetSettingInt(db, "kea_stats_puller_interval")
	require.NoError(t, err)
	require.EqualValues(t, 123, val)

	// get all settings
	settingsMap, err := GetAllSettings(db)
	require.NoError(t, err)
	require.EqualValues(t, 123, settingsMap["kea_stats_puller_interval"])
	require.Len(t, settingsMap, count)
}

// Check getting and setting settings.
func TestSettingsSetAndGet(t *testing.T) {
	db, _, teardown := dbtest.SetupDatabaseTestCase(t)
	defer teardown()

	// define some setting
	settings := []Setting{{
		Name:    "int_setting",
		ValType: SettingValTypeInt,
		Value:   "60",
	}, {
		Name:    "bool_setting",
		ValType: SettingValTypeBool,
		Value:   "true",
	}, {
		Name:    "str_setting",
		ValType: SettingValTypeStr,
		Value:   "some text",
	}, {
		Name:    "passwd_setting",
		ValType: SettingValTypePasswd,
		Value:   "HakunaM@t@ta",
	}}

	// add these settings to db
	for _, sTmp := range settings {
		s := sTmp
		_, err := db.Model(&s).Insert()
		require.NoError(t, err)
	}

	// check setting and getting int
	intVal, err := GetSettingInt(db, "int_setting")
	require.NoError(t, err)
	require.EqualValues(t, 60, intVal)

	err = SetSettingInt(db, "int_setting", 123)
	require.NoError(t, err)

	intVal, err = GetSettingInt(db, "int_setting")
	require.NoError(t, err)
	require.EqualValues(t, 123, intVal)

	// check setting and getting bool
	boolVal, err := GetSettingBool(db, "bool_setting")
	require.NoError(t, err)
	require.True(t, boolVal)

	err = SetSettingBool(db, "bool_setting", false)
	require.NoError(t, err)

	boolVal, err = GetSettingBool(db, "bool_setting")
	require.NoError(t, err)
	require.False(t, boolVal)

	err = SetSettingBool(db, "bool_setting", true)
	require.NoError(t, err)

	boolVal, err = GetSettingBool(db, "bool_setting")
	require.NoError(t, err)
	require.True(t, boolVal)

	// check setting and getting string
	strVal, err := GetSettingStr(db, "str_setting")
	require.NoError(t, err)
	require.EqualValues(t, "some text", strVal)

	err = SetSettingStr(db, "str_setting", "some new text")
	require.NoError(t, err)

	strVal, err = GetSettingStr(db, "str_setting")
	require.NoError(t, err)
	require.EqualValues(t, "some new text", strVal)

	// check setting and getting password
	pwdVal, err := GetSettingPasswd(db, "passwd_setting")
	require.NoError(t, err)
	require.EqualValues(t, "HakunaM@t@ta", pwdVal)

	err = SetSettingPasswd(db, "passwd_setting", "H@kErZ")
	require.NoError(t, err)

	pwdVal, err = GetSettingPasswd(db, "passwd_setting")
	require.NoError(t, err)
	require.EqualValues(t, "H@kErZ", pwdVal)
}
