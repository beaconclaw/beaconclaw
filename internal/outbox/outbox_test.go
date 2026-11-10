package outbox

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func seqs(o *Outbox) []uint64 {
	var out []uint64
	for _, e := range o.Pending() {
		out = append(out, e.Seq)
	}
	return out
}

func equal(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "outbox.json")

	first := Open(path, 10)
	for i := 0; i < 3; i++ {
		seq := first.NextSeq()
		if err := first.Add(seq, []byte(`{"n":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	first.Ack(2)

	second := Open(path, 10)
	if got := seqs(second); !equal(got, []uint64{1, 3}) {
		t.Errorf("after a restart the outbox holds %v, want [1 3]", got)
	}
	if next := second.NextSeq(); next != 4 {
		t.Errorf("sequence numbers must keep rising across a restart: got %d, want 4", next)
	}
}

func TestDropsTheOldestWhenFull(t *testing.T) {
	o := Open(filepath.Join(t.TempDir(), "outbox.json"), 3)
	for i := 0; i < 5; i++ {
		o.Add(o.NextSeq(), []byte(`{}`))
	}
	if got := seqs(o); !equal(got, []uint64{3, 4, 5}) {
		t.Errorf("a full outbox holds %v, want the newest [3 4 5]", got)
	}
}

func TestAckOfAnUnknownHeartbeatIsHarmless(t *testing.T) {
	o := Open(filepath.Join(t.TempDir(), "outbox.json"), 3)
	o.Add(o.NextSeq(), []byte(`{}`))
	if o.Ack(99) {
		t.Error("acknowledged a heartbeat that was never queued")
	}
	if o.Len() != 1 {
		t.Errorf("an unknown acknowledgement changed the outbox: %d waiting", o.Len())
	}
}

func TestAnUnreadableFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.json")
	if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	o := Open(path, 3)
	if o.Len() != 0 {
		t.Errorf("expected an empty outbox, got %d", o.Len())
	}
	if err := o.Add(o.NextSeq(), []byte(`{}`)); err != nil {
		t.Errorf("could not queue after a bad file: %v", err)
	}
}

func TestFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "outbox.json")
	o := Open(path, 3)
	o.Add(o.NextSeq(), []byte(`{}`))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("outbox file has mode %o, want 600", info.Mode().Perm())
	}
}
