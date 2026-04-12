package gps

// Types for the gpsd JSON TPV and SKY messages. These are parsed by the
// Client's reader goroutine and cached atomically on the matching
// DeviceClient so callers can read the latest snapshot without blocking
// on a channel.
//
// Field names and types come from the gpsd_json protocol reference:
// https://gpsd.gitlab.io/gpsd/gpsd_json.html. We keep only the fields
// the dashboard actually displays — parsing into a lean struct instead
// of a map avoids allocation on the hot path.

// FixMode values for TPV.Mode.
const (
	FixModeUnknown  = 0 // no mode value yet seen
	FixModeNoFix    = 1 // no fix
	FixMode2D       = 2 // 2D fix
	FixMode3D       = 3 // 3D fix
	FixModeTimeOnly = 4 // time-only fix (survey-in / fixed-mode receivers)
)

// TPV is a parsed gpsd TPV (time-position-velocity) report.
type TPV struct {
	Class       string  `json:"class"`
	Device      string  `json:"device"`
	Mode        int     `json:"mode"`
	Time        string  `json:"time"`
	LeapSeconds int     `json:"leapseconds"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	AltMSL      float64 `json:"altMSL"`
	AltHAE      float64 `json:"altHAE"`
	EPX         float64 `json:"epx"` // longitude error estimate, metres
	EPY         float64 `json:"epy"` // latitude error estimate, metres
	EPV         float64 `json:"epv"` // vertical error estimate, metres
	EPH         float64 `json:"eph"` // horizontal position error, metres (spherical)
	Sep         float64 `json:"sep"` // estimated spherical position error, metres
	Status      int     `json:"status"`
}

// Satellite is one entry in a SKY.Satellites list.
type Satellite struct {
	PRN    int     `json:"PRN"`
	GnssID int     `json:"gnssid"`
	SvID   int     `json:"svid"`
	Az     float64 `json:"az"`
	El     float64 `json:"el"`
	SS     float64 `json:"ss"` // signal strength (SNR) in dB-Hz
	Used   bool    `json:"used"`
	Health int     `json:"health"`
	Qual   int     `json:"qual"`
}

// SKY is a parsed gpsd SKY (satellite reception) report.
type SKY struct {
	Class      string      `json:"class"`
	Device     string      `json:"device"`
	Time       string      `json:"time"`
	GDOP       float64     `json:"gdop"`
	HDOP       float64     `json:"hdop"`
	PDOP       float64     `json:"pdop"`
	TDOP       float64     `json:"tdop"`
	VDOP       float64     `json:"vdop"`
	XDOP       float64     `json:"xdop"`
	YDOP       float64     `json:"ydop"`
	NSat       int         `json:"nSat"` // total satellites seen
	USat       int         `json:"uSat"` // satellites used in the current fix
	Satellites []Satellite `json:"satellites"`
}

// GNSSName returns a short name for a gpsd gnssid value. Values match
// u-blox's scheme (which gpsd passes through).
func GNSSName(id int) string {
	switch id {
	case 0:
		return "GPS"
	case 1:
		return "SBAS"
	case 2:
		return "Galileo"
	case 3:
		return "BeiDou"
	case 4:
		return "IMES"
	case 5:
		return "QZSS"
	case 6:
		return "GLONASS"
	default:
		return "?"
	}
}
