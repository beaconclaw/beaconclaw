package openclaw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeDoesNotFollowRedirects(t *testing.T) {
	var followed bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed = true
		w.WriteHeader(200)
	}))
	defer elsewhere.Close()

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	defer gateway.Close()

	p := NewProber(portFromServer(t, gateway))
	if p.checkHealth(context.Background()) {
		t.Error("a redirect was counted as healthy")
	}
	if followed {
		t.Error("the probe followed a redirect away from the gateway")
	}
}

func TestBoundNames(t *testing.T) {
	names := make([]string, maxFailingChannels+10)
	for i := range names {
		names[i] = strings.Repeat("x", maxChannelNameLen+50)
	}
	got := boundNames(names)
	if len(got) != maxFailingChannels {
		t.Errorf("kept %d names, want %d", len(got), maxFailingChannels)
	}
	if len(got[0]) != maxChannelNameLen {
		t.Errorf("kept a name of %d bytes, want %d", len(got[0]), maxChannelNameLen)
	}
}
