package workspace

import "testing"

func TestValidHostAlias(t *testing.T) {
	for _, ok := range []string{"box", "me@host.example", "prod-db", "h_1.x"} {
		if err := ValidHostAlias(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-oProxyCommand=x", "a b", "a;b", "a$(x)", "-p"} {
		if ValidHostAlias(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
