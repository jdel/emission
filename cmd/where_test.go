package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestWhereCmd(t *testing.T) {
	c := whereCmd()
	var buf bytes.Buffer
	c.SetOut(&buf)
	c.SetArgs([]string{})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	dataDir, _ := appScope.DataPath("")
	configDir, _ := appScope.ConfigPath("")
	for _, want := range []string{"data dir:    " + dataDir, "config dir:  " + configDir, "config file:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}
