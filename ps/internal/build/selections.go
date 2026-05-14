package build

import "errors"

type DeployMode int

const (
	DeployNone DeployMode = iota
	DeploySSH
	DeploySD
)

func (d DeployMode) String() string {
	switch d {
	case DeployNone:
		return "none"
	case DeploySSH:
		return "ssh"
	case DeploySD:
		return "sd"
	default:
		return "unknown"
	}
}

type Selections struct {
	Bitstream     bool
	Petalinux     bool
	PSSlipstream  bool
	PSHotswap     bool
	Deploy        DeployMode
	Reboot        bool
	SkipFSBLClean bool
	NoRestart     bool
	Eject         bool
}

var (
	ErrPSModesExclusive = errors.New("ps-slipstream and ps-hotswap are mutually exclusive")
	ErrRebootNeedsSSH   = errors.New("reboot only valid with --deploy=ssh")
	ErrEjectNeedsSD     = errors.New("eject only valid with --deploy=sd")
)

func (s Selections) Validate() error {
	if s.PSSlipstream && s.PSHotswap {
		return ErrPSModesExclusive
	}
	if s.Reboot && s.Deploy != DeploySSH {
		return ErrRebootNeedsSSH
	}
	if s.Eject && s.Deploy != DeploySD {
		return ErrEjectNeedsSD
	}
	return nil
}
