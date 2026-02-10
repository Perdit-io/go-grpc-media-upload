package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Perdit-io/media-upload/internal/server"
	pb "github.com/Perdit-io/media-upload/pb"
)

var (
	port     = flag.Int("port", 50051, "gRPC server port")
	httpPort = flag.Int("http", 8080, "HTTP server port")
	workers  = flag.Int("c", 2, "Number of consumer worker threads")
	queueLen = flag.Int("q", 5, "Max queue length (leaky bucket)")

	globalWP *server.WorkerPool
)

type grpcServer struct {
	pb.UnimplementedMediaUploadServer
	wp *server.WorkerPool
}

func (s *grpcServer) UploadVideo(stream pb.MediaUpload_UploadVideoServer) error {
	var file *os.File
	var videoID string
	var fileSize int

	for {
		req, err := stream.Recv()

		// 1. End of Stream (Upload Complete)
		if err == io.EOF {
			if file != nil {
				file.Close()
			}

			// Try to Enqueue
			job := server.Job{Filename: file.Name(), VideoID: videoID}
			added := s.wp.AddJob(job)

			if !added {
				slog.Warn("Queue full, dropping video", "id", videoID)
				os.Remove(job.Filename) // Cleanup dropped file
				return status.Errorf(codes.ResourceExhausted, "Server busy, queue full")
			}

			slog.Info("Upload Queued", "id", videoID, "size", fileSize)

			return stream.SendAndClose(&pb.UploadStatus{
				Message: "Upload successful",
				Success: true,
				VideoId: videoID,
			})
		}
		if err != nil {
			return status.Errorf(codes.Unknown, "Stream error: %v", err)
		}

		// 2. First Chunk (Create File)
		if file == nil {
			videoID = fmt.Sprintf("video_%s", filepath.Base(req.Filename))
			videoID = filepath.Base(videoID)
			savePath := filepath.Join("uploads", videoID)

			file, err = os.Create(savePath)
			if err != nil {
				return status.Errorf(codes.Internal, "Failed to create file: %v", err)
			}
			defer file.Close()
		}

		// 3. Write Chunk
		n, err := file.Write(req.Content)
		if err != nil {
			return status.Errorf(codes.Internal, "Write error: %v", err)
		}
		fileSize += n
	}
}

func main() {
	flag.Parse()

	if _, err := os.Stat("uploads"); os.IsNotExist(err) {
		os.Mkdir("uploads", 0755)
	}

	// 1. Start Worker Pool
	globalWP = server.NewWorkerPool(*workers, *queueLen)
	globalWP.Start()
	defer globalWP.Stop()

	// 2. Start HTTP Server (GUI)
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)
	// Serve uploaded videos/thumbnails
	http.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir("./uploads"))))
	// API Endpoint: Returns JSON list of videos
	http.HandleFunc("/api/videos", handleListVideos)
	// Web Upload Endpoint
	http.HandleFunc("/api/upload", handleWebUpload)

	go func() {
		slog.Info("HTTP GUI Running", "url", fmt.Sprintf("http://localhost:%d", *httpPort))
		if err := http.ListenAndServe(fmt.Sprintf(":%d", *httpPort), nil); err != nil {
			slog.Error("HTTP Server failed", "error", err)
		}
	}()

	// 3. Start gRPC Server
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		slog.Error("Failed to listen", "error", err)
		os.Exit(1)
	}

	s := grpc.NewServer()
	pb.RegisterMediaUploadServer(s, &grpcServer{wp: globalWP})

	slog.Info("gRPC Server Listening", "port", *port)
	if err := s.Serve(lis); err != nil {
		slog.Error("Failed to serve gRPC", "error", err)
	}
}

// Scans the upload folder and returns a list of video files
func handleListVideos(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir("uploads")
	if err != nil {
		http.Error(w, "Failed to read uploads", http.StatusInternalServerError)
		return
	}

	var videos []string
	for _, e := range entries {
		// Only send back .mp4 files (we infer the preview GIF path in JS)
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".mp4") {
			videos = append(videos, e.Name())
		}
	}

	// Respond with JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(videos)
}

// Handles file uploads from the Browser
func handleWebUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Parse Multipart Form (Max 50MB)
	r.ParseMultipartForm(50 << 20)

	file, header, err := r.FormFile("video")
	if err != nil {
		http.Error(w, "Invalid file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 2. Save File
	videoID := fmt.Sprintf("video_web_%s", filepath.Base(header.Filename))
	savePath := filepath.Join("uploads", videoID)

	dst, err := os.Create(savePath)
	if err != nil {
		http.Error(w, "Failed to create file", http.StatusInternalServerError)
		return
	}

	// Copy file content
	_, err = io.Copy(dst, file)
	dst.Close() // Close immediately so Worker can open it
	if err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	// 3. Send to Worker Pool (Leaky Bucket Check)
	job := server.Job{Filename: savePath, VideoID: videoID}
	added := globalWP.AddJob(job)

	if !added {
		slog.Warn("Web upload dropped (Queue Full)", "id", videoID)
		os.Remove(savePath)
		http.Error(w, "Server busy (Queue Full)", http.StatusServiceUnavailable)
		return
	}

	slog.Info("Web Upload Queued", "id", videoID)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Upload successful"))
}
