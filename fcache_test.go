package fcache

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// newTestCache creates a Cache backed by a temporary directory that is
// automatically cleaned up when the test finishes.
func newTestCache(t *testing.T) *Cache {
	t.Helper()
	c, err := New(t.TempDir(), time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestNew verifies that the constructor initialises the cache correctly and
// creates the backing directory when it does not already exist.
func TestNew(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subdir", "cache")

	c, err := New(dir, 2*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}

	if c.Dir != dir {
		t.Errorf("Dir = %q, want %q", c.Dir, dir)
	}
	if c.DiskExpiration != 2*time.Hour {
		t.Errorf("DiskExpiration = %v, want %v", c.DiskExpiration, 2*time.Hour)
	}
	if c.MemoryExpiration != 30*time.Minute {
		t.Errorf("MemoryExpiration = %v, want %v", c.MemoryExpiration, 30*time.Minute)
	}

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("backing directory was not created: %v", err)
	}
}

// TestNew_InvalidDir verifies that New returns an error when the directory
// cannot be created (e.g. parent path is a file, not a directory).
func TestNew_InvalidDir(t *testing.T) {
	// Create a file and then try to use it as a directory.
	f, err := os.CreateTemp(t.TempDir(), "file")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	f.Close()

	_, err = New(filepath.Join(f.Name(), "child"), time.Hour, time.Hour)
	if err == nil {
		t.Fatal("New: expected error for invalid dir, got nil")
	}
}

// TestCache_Set creates a new entry and verifies that the backing .fc file is
// written to disk with the correct content.
func TestCache_Set(t *testing.T) {
	c := newTestCache(t)
	data := []byte("hello, fcache")

	if err := c.Set("greet", data); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Confirm the .fc file exists and has the right content.
	content, err := os.ReadFile(filepath.Join(c.Dir, "greet.fc"))
	if err != nil {
		t.Fatalf("reading backing file: %v", err)
	}
	if !bytes.Equal(content, data) {
		t.Errorf("file content = %q, want %q", content, data)
	}
}

// TestCache_Set_SetsExpiration verifies that Set stamps the new Item with
// Unix timestamps derived from the cache's duration defaults.
func TestCache_Set_SetsExpiration(t *testing.T) {
	c := newTestCache(t)

	before := time.Now()
	if err := c.Set("key", []byte("v")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	after := time.Now()

	item := c.items["key"]
	if item == nil {
		t.Fatal("item not found in cache after Set")
	}

	wantLow := before.Add(c.DiskExpiration).Unix()
	wantHigh := after.Add(c.DiskExpiration).Unix()
	if item.DiskExpiration < wantLow || item.DiskExpiration > wantHigh {
		t.Errorf("DiskExpiration %d not in [%d, %d]", item.DiskExpiration, wantLow, wantHigh)
	}

	wantLow = before.Add(c.MemoryExpiration).Unix()
	wantHigh = after.Add(c.MemoryExpiration).Unix()
	if item.MemoryExpiration < wantLow || item.MemoryExpiration > wantHigh {
		t.Errorf("MemoryExpiration %d not in [%d, %d]", item.MemoryExpiration, wantLow, wantHigh)
	}
}

// TestCache_Get returns the data that was previously stored with Set.
func TestCache_Get(t *testing.T) {
	c := newTestCache(t)
	want := []byte("hello, fcache")

	if err := c.Set("greet", want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := c.Get("greet")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Get = %q, want %q", got, want)
	}
}

// TestCache_Get_NotFound verifies that getting a key that was never Set returns
// a non-nil error.
func TestCache_Get_NotFound(t *testing.T) {
	c := newTestCache(t)

	_, err := c.Get("missing")
	if err == nil {
		t.Fatal("Get: expected error for missing key, got nil")
	}
}

// TestCache_Set_Overwrite verifies that setting a key a second time updates the
// in-memory data.
func TestCache_Set_Overwrite(t *testing.T) {
	c := newTestCache(t)

	if err := c.Set("k", []byte("first")); err != nil {
		t.Fatalf("Set (first): %v", err)
	}
	if err := c.Set("k", []byte("second")); err != nil {
		t.Fatalf("Set (second): %v", err)
	}

	got, err := c.Get("k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, []byte("second")) {
		t.Errorf("Get after overwrite = %q, want %q", got, "second")
	}
}

// TestItem_Read_MemoryExpired verifies that when MemoryExpiration is -1 the
// item re-reads its data from the backing file rather than returning the stale
// in-memory copy.
func TestItem_Read_MemoryExpired(t *testing.T) {
	c := newTestCache(t)

	if err := c.Set("key", []byte("original")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	item := c.items["key"]

	// Overwrite the backing file with fresh content outside of the cache so
	// that it differs from item.Data.
	fresh := []byte("refreshed")
	if err := item.Handle.Truncate(0); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	if _, err := item.Handle.WriteAt(fresh, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if _, err := item.Handle.Seek(0, 0); err != nil {
		t.Fatalf("Seek: %v", err)
	}

	// Signal that the in-memory copy is stale.
	item.MemoryExpiration = -1

	got, err := item.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(got, fresh) {
		t.Errorf("Read after memory expiry = %q, want %q", got, fresh)
	}
}

// TestItem_Read_MemoryValid verifies that when MemoryExpiration is not -1 the
// item returns the cached in-memory data without touching the file.
func TestItem_Read_MemoryValid(t *testing.T) {
	c := newTestCache(t)
	want := []byte("cached")

	if err := c.Set("key", want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	item := c.items["key"]
	// Ensure MemoryExpiration is a normal future timestamp (not -1).
	item.MemoryExpiration = time.Now().Add(time.Hour).Unix()

	// Corrupt the backing file; Read must NOT fall through to the file.
	if err := item.Handle.Truncate(0); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	got, err := item.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Read = %q, want %q (in-memory data should be returned)", got, want)
	}
}

// TestCache_Concurrency hammers Set and Get from many goroutines simultaneously
// to surface any data races. Run with -race to get full coverage.
func TestCache_Concurrency(t *testing.T) {
	c := newTestCache(t)

	// Pre-populate a key so Get has something to retrieve.
	if err := c.Set("shared", []byte("initial")); err != nil {
		t.Fatalf("Set (setup): %v", err)
	}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	errs := make(chan error, goroutines)

	for i := range goroutines {
		go func(n int) {
			defer wg.Done()
			val := []byte{byte(n)}
			if err := c.Set("shared", val); err != nil {
				errs <- err
				return
			}
			if _, err := c.Get("shared"); err != nil {
				// Another goroutine may have replaced the key; that is fine.
				if !errors.Is(err, os.ErrNotExist) {
					errs <- err
				}
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}
}
