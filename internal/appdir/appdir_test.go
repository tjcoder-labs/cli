package appdir

import "testing"

func TestInstallIDStableAndRandom(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	id, created := InstallID()
	if !created || len(id) != 36 || id[14] != '4' {
		t.Fatalf("id=%q created=%v", id, created)
	}
	again, created := InstallID()
	if created || again != id {
		t.Fatal("install id must persist")
	}
	if NewUUID() == NewUUID() {
		t.Fatal("uuids must be random")
	}
}
