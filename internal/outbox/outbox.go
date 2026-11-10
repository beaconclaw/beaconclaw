// Package outbox holds heartbeats that have been collected but not yet
// acknowledged by the manager. It is kept on disk, so an outage, a restart of
// the agent or a reboot of the machine does not lose them.
package outbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Entry is one heartbeat waiting to be acknowledged.
type Entry struct {
	Seq  uint64          `json:"seq"`
	Data json.RawMessage `json:"data"`
}

type file struct {
	// LastSeq is the highest sequence number ever handed out, so numbers keep
	// rising across restarts and an acknowledgement can never match the wrong
	// heartbeat.
	LastSeq uint64  `json:"last_seq"`
	Entries []Entry `json:"entries"`
}

// Outbox is a bounded queue of unacknowledged heartbeats, oldest first. When
// it is full the oldest heartbeat is dropped to make room for the newest.
type Outbox struct {
	mu       sync.Mutex
	path     string
	capacity int
	state    file
}

// Open loads the outbox at path, or starts an empty one. A file that cannot be
// read is treated as empty: losing old heartbeats must never stop new ones.
func Open(path string, capacity int) *Outbox {
	if capacity < 1 {
		capacity = 1
	}
	o := &Outbox{path: path, capacity: capacity}

	if data, err := os.ReadFile(path); err == nil {
		var loaded file
		if json.Unmarshal(data, &loaded) == nil {
			o.state = loaded
		}
	}
	o.trim()
	return o
}

// NextSeq hands out the next sequence number.
func (o *Outbox) NextSeq() uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.state.LastSeq++
	return o.state.LastSeq
}

// Add queues a heartbeat and saves the outbox.
func (o *Outbox) Add(seq uint64, data []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if seq > o.state.LastSeq {
		o.state.LastSeq = seq
	}
	o.state.Entries = append(o.state.Entries, Entry{Seq: seq, Data: append(json.RawMessage(nil), data...)})
	o.trim()
	return o.save()
}

// Ack removes an acknowledged heartbeat and saves the outbox. It reports
// whether the heartbeat was waiting.
func (o *Outbox) Ack(seq uint64) bool {
	return o.remove(seq)
}

// Drop removes a heartbeat that is being given up on.
func (o *Outbox) Drop(seq uint64) bool {
	return o.remove(seq)
}

func (o *Outbox) remove(seq uint64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, entry := range o.state.Entries {
		if entry.Seq == seq {
			o.state.Entries = append(o.state.Entries[:i:i], o.state.Entries[i+1:]...)
			_ = o.save()
			return true
		}
	}
	return false
}

// Pending returns the waiting heartbeats, oldest first.
func (o *Outbox) Pending() []Entry {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]Entry(nil), o.state.Entries...)
}

// Len is how many heartbeats are waiting.
func (o *Outbox) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.state.Entries)
}

func (o *Outbox) trim() {
	if extra := len(o.state.Entries) - o.capacity; extra > 0 {
		o.state.Entries = append([]Entry(nil), o.state.Entries[extra:]...)
	}
}

// save writes the outbox beside its final place and moves it over in one
// step, so a crash mid-write leaves the previous file intact. Only this user
// may read it.
func (o *Outbox) save() error {
	if o.path == "" {
		return nil
	}
	data, err := json.Marshal(o.state)
	if err != nil {
		return err
	}
	dir := filepath.Dir(o.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".outbox-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), o.path)
}
