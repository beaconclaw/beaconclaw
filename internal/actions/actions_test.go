//go:build !noactions

package actions

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

func command(id, action string) Command {
	return Command{ID: id, AgentID: "agent-1", Action: action, ExpiresAt: now.Add(time.Minute)}
}

func TestACommandCarriesOnlyNamesAndTimes(t *testing.T) {
	// A field that could hold a command line or arguments must never be added.
	fields := map[string]bool{}
	typ := reflect.TypeOf(Command{})
	for i := 0; i < typ.NumField(); i++ {
		fields[typ.Field(i).Name] = true
	}
	want := map[string]bool{"ID": true, "AgentID": true, "Action": true, "ExpiresAt": true}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("Command has fields %v, want exactly %v", fields, want)
	}
}

func TestNothingIsAllowedUntilSwitchedOn(t *testing.T) {
	gate := NewGate(nil)
	for _, action := range Known() {
		err := gate.Check(command("id-"+action, action), "agent-1", now)
		if err == nil || !strings.Contains(err.Error(), "not switched on") {
			t.Errorf("%s with nothing switched on: got %v, want a refusal", action, err)
		}
	}
}

func TestOnlyWhatWasSwitchedOnIsAllowed(t *testing.T) {
	gate := NewGate([]string{Diagnostics, "format_disk"})
	if err := gate.Check(command("a", Diagnostics), "agent-1", now); err != nil {
		t.Fatalf("diagnostics, switched on: %v", err)
	}
	if err := gate.Check(command("b", RestartGateway), "agent-1", now); err == nil {
		t.Fatal("restart was allowed though only diagnostics is switched on")
	}
	// A name this build does not know is refused even if the settings list it.
	if err := gate.Check(command("c", "format_disk"), "agent-1", now); err == nil || !strings.Contains(err.Error(), "not an action") {
		t.Fatalf("unknown action: got %v", err)
	}
}

func TestRefusesAnotherAgentsCommandAnExpiredOneAndARepeat(t *testing.T) {
	gate := NewGate(Known())

	if err := gate.Check(command("a", Diagnostics), "agent-2", now); err == nil || !strings.Contains(err.Error(), "another agent") {
		t.Errorf("wrong agent: got %v", err)
	}
	// An agent that was never told its own ID accepts nothing.
	if err := gate.Check(command("a", Diagnostics), "", now); err == nil {
		t.Error("a command was accepted before the agent knew its own ID")
	}

	expired := command("b", Diagnostics)
	expired.ExpiresAt = now.Add(-time.Second)
	if err := gate.Check(expired, "agent-1", now); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired: got %v", err)
	}
	endless := command("c", Diagnostics)
	endless.ExpiresAt = now.Add(24 * time.Hour)
	if err := gate.Check(endless, "agent-1", now); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("valid for a day: got %v", err)
	}
	if err := gate.Check(Command{AgentID: "agent-1", Action: Diagnostics, ExpiresAt: now.Add(time.Minute)}, "agent-1", now); err == nil {
		t.Error("a command with no ID was accepted")
	}

	if err := gate.Check(command("d", Diagnostics), "agent-1", now); err != nil {
		t.Fatalf("first time: %v", err)
	}
	if err := gate.Check(command("d", Diagnostics), "agent-1", now.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "already received") {
		t.Errorf("repeat: got %v", err)
	}
	// A refused command is not remembered: nothing was done, so there is nothing to protect.
	if err := gate.Check(command("b", Diagnostics), "agent-1", now); err != nil {
		t.Errorf("an ID first seen on a refused command should still be usable: %v", err)
	}
}

func TestOneRestartEveryFiveMinutesWhateverTheManagerSays(t *testing.T) {
	gate := NewGate(Known())
	if err := gate.Check(command("a", RestartGateway), "agent-1", now); err != nil {
		t.Fatalf("first restart: %v", err)
	}
	later := command("b", RestartGateway)
	later.ExpiresAt = now.Add(3 * time.Minute)
	if err := gate.Check(later, "agent-1", now.Add(2*time.Minute)); err == nil || !strings.Contains(err.Error(), "restarted less than") {
		t.Errorf("second restart after two minutes: got %v", err)
	}
	// Diagnostics is not held back by it.
	check := command("c", Diagnostics)
	check.ExpiresAt = now.Add(3 * time.Minute)
	if err := gate.Check(check, "agent-1", now.Add(2*time.Minute)); err != nil {
		t.Errorf("diagnostics after a restart: %v", err)
	}
	after := command("d", RestartGateway)
	after.ExpiresAt = now.Add(7 * time.Minute)
	if err := gate.Check(after, "agent-1", now.Add(6*time.Minute)); err != nil {
		t.Errorf("restart after six minutes: %v", err)
	}
}

func TestNormalizeKeepsOnlyKnownNamesOnceInOrder(t *testing.T) {
	got := Normalize([]string{RestartGateway, "reboot", Diagnostics, RestartGateway})
	if want := []string{Diagnostics, RestartGateway}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if len(Normalize(nil)) != 0 {
		t.Fatal("nothing in, something out")
	}
}
