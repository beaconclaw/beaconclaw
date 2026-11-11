package main

import "testing"

func TestWaitingHeartbeats(t *testing.T) {
	cases := map[int]string{
		0:  "none, the manager has confirmed every heartbeat",
		1:  "1 heartbeat not yet confirmed by the manager",
		42: "42 heartbeats not yet confirmed by the manager",
	}
	for count, want := range cases {
		if got := waitingHeartbeats(count); got != want {
			t.Errorf("waitingHeartbeats(%d) = %q, want %q", count, got, want)
		}
	}
}
