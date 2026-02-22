package dbus

import (
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"
)

const (
	fdoName                   = `org.freedesktop.DBus`
	fdoPath                   = dbus.ObjectPath(`/org/freedesktop/DBus`)
	fdoMemberNameOwnerChanged = `NameOwnerChanged`
	fdoSignalNameOwnerChanged = fdoName + `.` + fdoMemberNameOwnerChanged
	fdoIntrospectableName     = fdoName + `.Introspectable`

	fdoPropertiesName                    = fdoName + `.Properties`
	fdoPropertiesMethodGetAll            = fdoPropertiesName + `.GetAll`
	fdoPropertiesMemberPropertiesChanged = `PropertiesChanged`
	fdoPropertiesSignalPropertiesChanged = fdoPropertiesName + `.` + fdoPropertiesMemberPropertiesChanged

	fdoLogindName = `org.freedesktop.login1`
	fdoLogindPath = `/org/freedesktop/login1`

	fdoLogindManagerName          = fdoLogindName + `.Manager`
	fdoLogindManagerMethodInhibit = fdoLogindManagerName + `.Inhibit`

	fdoLogindSessionName                = fdoLogindName + `.Session`
	fdoLogindSessionPath                = fdoLogindPath + `/session/auto`
	fdoLogindSessionMethodSetBrightness = fdoLogindSessionName + `.SetBrightness`

	fdoSystemdName       = `org.freedesktop.systemd1`
	fdoSystemdUnitPath   = `/org/freedesktop/systemd1/unit`
	fdoSystemdDeviceName = `org.freedesktop.systemd1.Device`

	fdoUPowerName                   = `org.freedesktop.UPower`
	fdoUPowerPath                   = `/org/freedesktop/UPower`
	fdoUPowerMethodGetDisplayDevice = fdoUPowerName + `.GetDisplayDevice`

	fdoMediaPlayerPath = `/org/mpris/MediaPlayer2`
	fdoMediaPlayerName = `org.mpris.MediaPlayer2`
	fdoPlayerName      = fdoMediaPlayerName + ".Player"

	fdoPlayerMethodPlayPause   = fdoPlayerName + `.PlayPause`
	fdoPlayerMethodPlay        = fdoPlayerName + `.Play`
	fdoPlayerMethodPause       = fdoPlayerName + `.Pause`
	fdoPlayerMethodNext        = fdoPlayerName + `.Next`
	fdoPlayerMethodPrevious    = fdoPlayerName + `.Previous`
	fdoPlayerMethodStop        = fdoPlayerName + `.Stop`
	fdoPlayerMethodSeek        = fdoPlayerName + `.Seek`
	fdoPlayerMethodSetPosition = fdoPlayerName + `.SetPosition`

	fdoPlayerPropertyPlaybackStatus = `PlaybackStatus`
	fdoPlayerPropertyCanGoNext      = `CanGoNext`
	fdoPlayerPropertyCanGoPrevious  = `CanGoPrevious`
	fdoPlayerPropertyIdentity       = `Identity`
	fdoPlayerPropertyDesktopEntry   = `DesktopEntry`
	fdoPlayerPropertyMetadata       = `Metadata`

	fdoUPowerDeviceName                = fdoUPowerName + `.Device`
	fdoUPowerDevicePropertyVendor      = `Vendor`
	fdoUPowerDevicePropertyModel       = `Model`
	fdoUPowerDevicePropertyType        = `Type`
	fdoUPowerDevicePropertyPowerSupply = `PowerSupply`
	fdoUPowerDevicePropertyOnline      = `Online`
	fdoUPowerDevicePropertyTimeToEmpty = `TimeToEmpty`
	fdoUPowerDevicePropertyTimeToFull  = `TimeToFull`
	fdoUPowerDevicePropertyPercentage  = `Percentage`
	fdoUPowerDevicePropertyIsPresent   = `IsPresent`
	fdoUPowerDevicePropertyState       = `State`
	fdoUPowerDevicePropertyIconName    = `IconName`
	fdoUPowerDevicePropertyEnergy      = `Energy`
	fdoUPowerDevicePropertyEnergyEmpty = `EnergyEmpty`
	fdoUPowerDevicePropertyEnergyFull  = `EnergyFull`

	fdoIdleInhibitorPropertyShutdown = `shutdown`
	fdoIdleInhibitorPropertySleep    = `sleep`
	fdoIdleInhibitorPropertyIdle     = `idle`
)

func systemdUnitToObjectPath(unitName string) (dbus.ObjectPath, error) {
	unitName = strings.ReplaceAll(unitName, `-`, `\x2d`)

	var result strings.Builder
	for _, r := range unitName {
		if !isValidObjectPathChar(r) {
			if _, err := result.WriteString(fmt.Sprintf("%d", r)); err != nil {
				return ``, err
			}
			continue
		}

		if _, err := result.WriteRune(r); err != nil {
			return ``, err
		}
	}

	return dbus.ObjectPath(result.String()), nil
}
