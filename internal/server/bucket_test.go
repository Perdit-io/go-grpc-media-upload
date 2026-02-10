package server

import (
	"testing"
	"time"
)

func TestWorkerPoolCapacity(t *testing.T) {
	queueSize := 3
	jobQueue := make(chan int, queueSize)

	for i := 0; i < queueSize; i++ {
		select {
		case jobQueue <- i:
		default:
			t.Errorf("Queue should not be full yet at index %d", i)
		}
	}

	select {
	case jobQueue <- 99:
		t.Errorf("Queue should be full! The Leaky Bucket logic failed.")
	default:
	}
}

func TestProcessingDelay(t *testing.T) {
	start := time.Now()
	time.Sleep(10 * time.Millisecond)
	elapsed := time.Since(start)

	if elapsed < 10*time.Millisecond {
		t.Errorf("Time tracking is broken")
	}
}
