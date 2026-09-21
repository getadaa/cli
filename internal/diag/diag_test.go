package diag

import (
	"context"
	"strings"
	"testing"
)

func TestCollectHasSections(t *testing.T) {
	out := Collect(context.Background(), "localhost")
	for _, want := range []string{"Hostname:", "System:", "Network interfaces", "Name lookup", "localhost"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	t.Log(out)
}
