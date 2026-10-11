package composition

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStoreLastSurvivesBackwardsClock(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	// Seed a previously completed save whose wall clock was ahead of the
	// current clock. No process clock or existing user state is changed.
	old := Record{Version: RecordVersion, RunID: newRunID(time.Now().Add(time.Hour)), Verdict: "pass"}
	dir := filepath.Join(s.Dir, old.RunID)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	current := Record{Verdict: "fail"}
	if err := s.Save(&current, func() any { return current }); err != nil {
		t.Fatal(err)
	}
	last, err := s.Load("last")
	if err != nil || last.RunID != current.RunID || last.Verdict != "fail" {
		t.Fatalf("last selected an older save after clock reversal: got %+v, current %+v, error %v", last, current, err)
	}
}

func TestStoreConcurrentSavesKeepAllocationOrder(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	started, finish := make(chan struct{}), make(chan struct{})
	errors := make(chan error, 1)
	first := Record{Verdict: "pass"}
	go func() {
		errors <- s.Save(&first, func() any {
			close(started)
			<-finish
			return first
		})
	}()
	<-started
	second := Record{Verdict: "fail"}
	err := (Store{Dir: s.Dir}).Save(&second, func() any { return second })
	close(finish)
	if firstErr := <-errors; firstErr != nil {
		t.Fatal(firstErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	last, err := s.Load("last")
	if err != nil || last.RunID != second.RunID {
		t.Fatalf("later completion replaced newer allocated run: %+v %v", last, err)
	}
}

func TestStoreFrozenClockReservationsAreDistinct(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[time.Time]bool{}
	var release []func()
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stamp, cleanup, err := (Store{Dir: dir}).reserveRunTime(now)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if seen[stamp] {
				t.Errorf("two writers reserved %s", stamp)
			}
			seen[stamp] = true
			release = append(release, cleanup)
		}()
	}
	wg.Wait()
	for _, cleanup := range release {
		cleanup()
	}
	if len(seen) != 32 {
		t.Fatalf("reserved %d distinct timestamps", len(seen))
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("reservations left behind: %v %v", entries, err)
	}
}

func TestStoreRejectsReservationPublishedAfterInitialScan(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	next := time.Now().UTC().Truncate(time.Millisecond)
	// A delayed writer scanned an empty store and chose next. Another writer
	// then reserved, published and released exactly that timestamp.
	release, err := s.tryReserveRunTime(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(s.Dir, newRunID(next)), 0700); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	if cleanup, err := s.tryReserveRunTime(next); !errors.Is(err, os.ErrExist) {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("delayed writer reused a published timestamp: %v", err)
	}
	allocated, cleanup, err := s.reserveRunTime(next)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !allocated.After(next) {
		t.Fatalf("retry did not advance allocation: %s <= %s", allocated, next)
	}
}
