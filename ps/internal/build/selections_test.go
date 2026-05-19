package build

import "testing"

func TestSelectionsValidate(t *testing.T) {
	cases := []struct {
		name string
		sel  Selections
		ok   bool
	}{
		{"empty plan", Selections{}, true},
		{"bitstream only", Selections{Bitstream: true}, true},
		{"yocto only", Selections{Yocto: true}, true},
		{"both ps modes mutually exclusive", Selections{PSSlipstream: true, PSHotswap: true}, false},
		{"reboot without ssh deploy", Selections{Bitstream: true, Reboot: true}, false},
		{"reboot with ssh", Selections{Bitstream: true, Deploy: DeploySSH, Reboot: true}, true},
		{"eject requires sd deploy", Selections{Bitstream: true, Eject: true}, false},
	}
	for _, c := range cases {
		err := c.sel.Validate()
		if (err == nil) != c.ok {
			t.Errorf("%s: Validate err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestDeployModeString(t *testing.T) {
	if DeployNone.String() != "none" || DeploySSH.String() != "ssh" || DeploySD.String() != "sd" {
		t.Fatalf("DeployMode.String mismatch")
	}
}
