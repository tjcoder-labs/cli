package update

import (
	"testing"

	"github.com/tjcoder-labs/cli/internal/appdir"
)

func TestAutoUpdateDefaultsOn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(AutoUpdateEnv, "")
	if ok, why := AutoUpdateEnabled(); !ok || why != "" {
		t.Fatalf("default: got %v %q, want on", ok, why)
	}
	if !AutoUpdateSetting() {
		t.Fatal("nil setting must read as on")
	}
	var s appdir.Settings
	if s.AutoUpdate != nil {
		t.Fatal("zero Settings must leave AutoUpdate nil")
	}
}

func TestAutoUpdateSettingAndEnvOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(AutoUpdateEnv, "")
	off := false
	if _, err := appdir.Update(func(s *appdir.Settings) { s.AutoUpdate = &off }); err != nil {
		t.Fatal(err)
	}
	if ok, why := AutoUpdateEnabled(); ok || why != "config" {
		t.Fatalf("config off: got %v %q", ok, why)
	}
	if AutoUpdateSetting() {
		t.Fatal("persisted false must read as off")
	}
	on := true
	if _, err := appdir.Update(func(s *appdir.Settings) { s.AutoUpdate = &on }); err != nil {
		t.Fatal(err)
	}
	if ok, _ := AutoUpdateEnabled(); !ok {
		t.Fatal("config on must be on")
	}
	for _, v := range []string{"0", "false", "no", "FALSE"} {
		t.Setenv(AutoUpdateEnv, v)
		if ok, why := AutoUpdateEnabled(); ok || why != AutoUpdateEnv {
			t.Fatalf("%s=%q: got %v %q, want forced off", AutoUpdateEnv, v, ok, why)
		}
	}
	t.Setenv(AutoUpdateEnv, "1")
	if ok, _ := AutoUpdateEnabled(); !ok {
		t.Fatal("CODER_AUTO_UPDATE=1 must not turn it off")
	}
}

func TestDecide(t *testing.T) {
	newer := Result{Current: "0.9.185", Latest: "0.9.186"}
	same := Result{Current: "0.9.186", Latest: "0.9.186"}
	cases := []struct {
		res    Result
		method Method
		auto   bool
		want   Action
	}{
		{same, MethodNPM, true, ActionNone},
		{same, MethodSource, true, ActionNone},
		{Result{Current: "0.9.185"}, MethodBinary, true, ActionNone},
		{newer, MethodNPM, true, ActionInstall},
		{newer, MethodBinary, true, ActionInstall},
		{newer, MethodSource, true, ActionSourceNotice},
		{newer, MethodNPM, false, ActionNotify},
		{newer, MethodBinary, false, ActionNotify},
		{newer, MethodSource, false, ActionNotify},
		{newer, Method("weird"), true, ActionNotify},
	}
	for _, c := range cases {
		if got := Decide(c.res, c.method, c.auto); got != c.want {
			t.Errorf("Decide(%v→%v, %s, auto=%v) = %s, want %s", c.res.Current, c.res.Latest, c.method, c.auto, got, c.want)
		}
	}
}
