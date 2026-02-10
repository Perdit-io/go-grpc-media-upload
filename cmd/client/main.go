package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/Perdit-io/media-upload/pb"
)

var (
	serverAddr = flag.String("addr", "127.0.0.1:50051", "The server address in the format of host:port")
	producers  = flag.Int("p", 1, "Number of concurrent producer threads")
	sourceDir  = flag.String("dir", "test_data", "Root directory for source videos")
)

const ChunkSize = 64 * 1024 // 64KB chunks

const SampleVideoURL = "https://test-videos.co.uk/vids/bigbuckbunny/mp4/h264/360/Big_Buck_Bunny_360_10s_1MB.mp4"

func main() {
	flag.Parse()

	// 1. Setup Dummy Data
	if err := setupDummyData(*sourceDir, *producers); err != nil {
		slog.Error("Failed to setup test data", "error", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup

	// 2. Start Producers
	start := time.Now()
	for i := range *producers {
		wg.Add(1)
		go runProducer(i, &wg)
	}

	wg.Wait()
	slog.Info("All producers finished", "duration", time.Since(start))
}

func runProducer(id int, wg *sync.WaitGroup) {
	defer wg.Done()

	myFolder := filepath.Join(*sourceDir, fmt.Sprintf("producer_%d", id))

	// 3. Connect to gRPC Server
	conn, err := grpc.NewClient(*serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		slog.Error("Failed to connect", "producer", id, "error", err)
		return
	}
	defer conn.Close()

	client := pb.NewMediaUploadClient(conn)

	// 4. Walk the folder and upload everything inside
	files, _ := os.ReadDir(myFolder)
	for _, entry := range files {
		if entry.IsDir() {
			continue
		}

		filePath := filepath.Join(myFolder, entry.Name())
		uploadFile(client, id, filePath)
	}
}

func uploadFile(client pb.MediaUploadClient, producerID int, path string) {
	file, err := os.Open(path)
	if err != nil {
		slog.Error("Failed to open file", "error", err)
		return
	}
	defer file.Close()

	// Context Timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := client.UploadVideo(ctx)
	if err != nil {
		slog.Error("Failed to create stream", "error", err)
		return
	}

	// 5. Stream the chunks
	buf := make([]byte, ChunkSize)
	slog.Info("Starting Upload", "producer", producerID, "file", path)

	for {
		n, err := file.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Error("File read error", "error", err)
			return
		}

		req := &pb.VideoChunk{
			Filename: filepath.Base(path),
			Content:  buf[:n],
		}

		if err := stream.Send(req); err != nil {
			slog.Error("Send failed (Server likely busy)", "producer", producerID, "error", err)
			return
		}
	}

	// 6. Close and Receive Response
	res, err := stream.CloseAndRecv()
	if err != nil {
		slog.Error("Upload failed", "producer", producerID, "error", err)
		return
	}

	slog.Info("Upload Success", "producer", producerID, "server_msg", res.Message, "video_id", res.VideoId)
}

func setupDummyData(root string, count int) error {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		os.Mkdir(root, 0755)
	}

	masterSample := filepath.Join(root, "sample_source.mp4")
	if _, err := os.Stat(masterSample); os.IsNotExist(err) {
		slog.Info("Downloading sample video from web... (this happens once)")
		resp, err := http.Get(SampleVideoURL)
		if err != nil {
			return fmt.Errorf("failed to download sample: %v", err)
		}
		defer resp.Body.Close()

		out, err := os.Create(masterSample)
		if err != nil {
			return fmt.Errorf("failed to create sample file: %v", err)
		}
		defer out.Close()

		_, err = io.Copy(out, resp.Body)
		if err != nil {
			return fmt.Errorf("failed to save sample file: %v", err)
		}
		slog.Info("Sample video downloaded successfully")
	}

	for i := range count {
		subDir := filepath.Join(root, fmt.Sprintf("producer_%d", i))
		os.Mkdir(subDir, 0755)

		// Create 2 dummy files per producer
		for j := range 2 {
			fname := filepath.Join(subDir, fmt.Sprintf("vid_%d_%d.mp4", i, j+1))
			if _, err := os.Stat(fname); os.IsNotExist(err) {
				// Copy from master sample
				source, _ := os.Open(masterSample)
				dest, _ := os.Create(fname)
				io.Copy(dest, source)
				source.Close()
				dest.Close()
			}
		}
	}
	return nil
}
