package server

import (
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
)

type Job struct {
	Filename string
	VideoID  string
}

type WorkerPool struct {
	JobQueue    chan Job // Leaky Bucket
	WorkerCount int
	wg          sync.WaitGroup
}

func NewWorkerPool(c int, q int) *WorkerPool {
	return &WorkerPool{
		JobQueue:    make(chan Job, q),
		WorkerCount: c,
	}
}

func (wp *WorkerPool) Start() {
	for i := 1; i <= wp.WorkerCount; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}
	slog.Info("Worker Pool Started", "workers", wp.WorkerCount)
}

func (wp *WorkerPool) AddJob(job Job) bool {
	select {
	case wp.JobQueue <- job:
		return true // Success
	default:
		return false // Queue Full (Leak)
	}
}

// Stop waits for workers to finish
func (wp *WorkerPool) Stop() {
	close(wp.JobQueue)
	wp.wg.Wait()
}

func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()

	for job := range wp.JobQueue {
		slog.Info("Processing Video", "worker", id, "file", job.Filename)

		inputFile := job.Filename
		outputGif := fmt.Sprintf("uploads/%s_preview.gif", job.VideoID)
		outputJpg := fmt.Sprintf("uploads/%s_thumbnail.jpg", job.VideoID)

		// Generates a 5-second GIF, scaled to 320px width
		cmdGif := exec.Command("ffmpeg", "-y", "-i", inputFile, "-t", "5", "-vf", "fps=10,scale=320:-1", outputGif)
		cmdGif.Stdout = nil
		cmdGif.Stderr = nil

		if err := cmdGif.Run(); err != nil {
			slog.Error("FFmpeg GIF generation failed", "worker", id, "error", err)
			continue
		}

		// Generate JPG (Static Thumbnail) snapshot at the 1-second mark
		cmdJpg := exec.Command("ffmpeg", "-y", "-i", inputFile, "-ss", "00:00:01", "-vframes", "1", "-vf", "scale=320:-1", outputJpg)
		cmdJpg.Stdout = nil
		cmdJpg.Stderr = nil

		if err := cmdJpg.Run(); err != nil {
			slog.Error("FFmpeg JPG generation failed", "worker", id, "error", err)
			continue
		}

		slog.Info("Video Processed", "worker", id, "gif", outputGif, "jpg", outputJpg)
	}
}
