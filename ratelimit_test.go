package ratelimit

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/uplo-tech/fastrand"
	"github.com/uplo-tech/log"
	"github.com/uplo-tech/uplomux"
)

// TestRLSimpleWriteRead tests a simple rate-limited write and read operation.
func TestRLSimpleWriteRead(t *testing.T) {
	// Set limits
	packetSize := uint64(64)
	bps := int64(1000)
	rl := NewRateLimit(bps, bps, packetSize)

	// Create a io.ReadWriter.
	rw := bytes.NewBuffer(make([]byte, 0))

	// Wrap it into a rate limited ReadWriter.
	c := make(chan struct{})
	defer close(c)
	rlc := NewRLReadWriter(rw, rl, c)

	// Create 1mb to write.
	data := fastrand.Bytes(1000)

	// Write data while measuring time.
	start := time.Now()
	n, err := rlc.Write(data)
	d := time.Since(start)

	// Check for errors
	if n < len(data) {
		t.Error("Not whole data was written")
	}
	if err != nil {
		t.Error("Failed to write data", err)
	}
	// Check the duration. We need to subtract packetSize since the time will
	// be off by one packet. That's because the last written packet will finish
	// faster than anticipated.
	if d.Seconds() < float64(uint64(len(data))-packetSize)/float64(bps) {
		t.Error("Write didn't take long enough", d.Seconds())
	}

	// Read data back from file while measuring time.
	readData := make([]byte, len(data))
	start = time.Now()
	n, err = rlc.Read(readData)
	d = time.Since(start)

	// Check for errors
	if n < len(data) {
		t.Error("Not whole data was read")
	}
	if err != nil {
		t.Error("Failed to read data", err)
	}
	// Check the duration again. Should be the same time.
	if d.Seconds() < float64(uint64(len(data))-packetSize)/float64(bps) {
		t.Error("Read didn't take long enough", d.Seconds())
	}
	// Check if the read data is the same as the written one.
	if bytes.Compare(readData, data) != 0 {
		t.Error("Read data doesn't match written data")
	}
}

// TestRLParallelWriteRead tests a parallel rate-limited write and read operations.
func TestRLParallelWriteRead(t *testing.T) {
	// Set limits
	bps := int64(1000)
	bytesToWrite := int(bps)
	rl := NewRateLimit(bps, bps, 4096)

	// f creates a rate limited buffer, writes some data to it and reads it
	// afterwards.
	f := func() {
		// Create a io.ReadWriter.
		rw := bytes.NewBuffer(make([]byte, 0))

		// Wrap it into a rate limited ReadWriter.
		c := make(chan struct{})
		defer close(c)
		rlc := NewRLReadWriter(rw, rl, c)

		// Create 1mb to write.
		data := fastrand.Bytes(bytesToWrite)

		// Write data while measuring time.
		n, err := rlc.Write(data)

		// Check for errors
		if n < len(data) {
			t.Error("Not whole data was written")
		}
		if err != nil {
			t.Error("Failed to write data", err)
		}

		// Read data back from file while measuring time.
		readData := make([]byte, len(data))
		n, err = rlc.Read(readData)

		// Check for errors
		if n < len(data) {
			t.Error("Not whole data was read")
		}
		if err != nil {
			t.Error("Failed to read data", err)
		}
		// Check if the read data is the same as the written one.
		if bytes.Compare(readData, data) != 0 {
			t.Error("Read data doesn't match written data")
		}
	}
	// Start a few threads and wait for them to finish.
	var wg sync.WaitGroup
	start := time.Now()
	numThreads := 10
	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func() {
			f()
			wg.Done()
		}()
	}
	wg.Wait()
	d := time.Since(start)

	// d should be around 9 seconds. Each threads reads and writes exactly 1
	// second of data but the first thread can write instantly.
	if d.Seconds() < float64(numThreads-1) || d.Seconds() > float64(numThreads) {
		t.Fatalf("Test should run between %v and %v seconds but was %v", numThreads-1, numThreads, d.Seconds())
	}
}

// TestNewRatelimitStream tests the ratelimit on a uplomux.Stream.
func TestNewRatelimitStream(t *testing.T) {
	// Create a uplomux.
	sm, err := uplomux.New("localhost:0", "localhost:0", log.DiscardLogger, filepath.Join(os.TempDir(), t.Name()))
	if err != nil {
		t.Fatal(err)
	}

	// Create a new ratelimiter.
	seconds := 5
	data := fastrand.Bytes(10)
	throughput := int64(len(data) / seconds)
	rl := NewRateLimit(throughput, throughput, 0)

	// Register a listener.
	var wg sync.WaitGroup
	wg.Add(1)
	err = sm.NewListener("test", func(stream uplomux.Stream) {
		defer wg.Done()

		// Wrap the stream.
		stream = NewRLStream(stream, rl, make(chan struct{}))

		// Read some data.
		_, err := stream.Read(make([]byte, 2*len(data)))
		if err != nil {
			t.Error(err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a new stream.
	stream, err := sm.NewStream("test", sm.Address().String(), sm.PublicKey())
	if err != nil {
		t.Fatal(err)
	}

	// Wrap the stream.
	stream = NewRLStream(stream, rl, make(chan struct{}))

	// Get time.
	start := time.Now()

	// Write some data. We do this twice since the first one won't be blocked
	// by the ratelimit but the second one will be.
	_, err = stream.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	_, err = stream.Write(data)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for everything to complete and check that the test took at least the
	// expected amount of time.
	wg.Wait()
	if s := time.Since(start).Seconds(); s < float64(seconds) {
		t.Fatalf("test only took %v seconds", s)
	}
}
