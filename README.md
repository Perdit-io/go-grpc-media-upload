# Distributed Media Upload Service

A high-performance, concurrent Producer-Consumer system simulating an IoT-to-Cloud video processing pipeline. Built with **Go (Golang)**, **gRPC**, **Protocol Buffers**, and **Docker**.

## Overview

This system simulates a distributed architecture where multiple "Edge Devices" (Producers) concurrently upload video footage to a central "Cloud Server" (Consumer). The server manages load using a **Leaky Bucket** queueing algorithm, processes videos using **FFmpeg** to generate previews, and serves a real-time **Web Dashboard**.

### Key Architecture

  * **Producer (Client):** Simulates concurrent IoT devices streaming video data via gRPC (HTTP/2).
  * **Consumer (Server):** A Hybrid Server (gRPC + HTTP) that uses a **Worker Pool** to process videos and protect system resources.
  * **Viewer (GUI):** A browser-based dashboard for real-time monitoring and playback.

-----

## Features

### Core Requirements

  * **Concurrent Uploads:** Spawns `p` producer threads reading from isolated folders.
  * **Worker Pool Pattern:** Server uses `c` fixed workers to process videos without resource exhaustion.
  * **Leaky Bucket Queue:** Implements a fixed-size buffer `q`. Requests exceeding capacity are immediately rejected (Fail Fast).
  * **Video Processing:** Generates **GIF previews** and **JPG thumbnails** using FFmpeg.
  * **gRPC Streaming:** Uses client-side streaming to upload large files with low memory footprint (64KB chunks).

### Advanced Features

  * **Fully Containerized:** Docker & Docker Compose setup ensures reproducibility (no need to install Go/FFmpeg manually).
  * **Infrastructure as Code:** Test scenarios (Happy Path vs. Stress Test) defined via Docker Compose configurations.
  * **Hybrid Server:** Handles gRPC traffic and Web Traffic (HTTP/1.1) simultaneously on different ports.
  * **Web Uploader:** Drag-and-drop web interface for uploading files alongside the CLI client.
  * **Resilient Frontend:** Auto-retry logic for loading thumbnails to handle processing delays.
  * **Automated Test Data:** Client automatically downloads valid sample video data on first run.
  
-----

## System Design

### The Leaky Bucket (Queue)
I implemented the rate-limiting logic using a **Non-Blocking Buffered Channel** in Go.
* **Incoming Request:** The server attempts to push the job into `make(chan Job, q)`.
* **Select/Default:** If the channel is full, the `default` case triggers immediately, returning a `ResourceExhausted` gRPC error to the client. This prevents memory overflow during traffic spikes.

```text
[ Client ] --(Stream)--> [ gRPC Handler ]
                               |
                        [ Leaky Bucket ] <--- (Capacity = Q)
                               |
                     [ Check Capacity? ]
                     /                \
              (Full) /                  \ (Space Available)
                    /                    \
           [ Reject ]           [ Worker Pool ]
                                   |  |  |
                                [ FFmpeg ] [ FFmpeg ]
```

### Concurrency Model
Instead of OS threads, I utilized **Goroutines** (M:N scheduling) for the Worker Pool. This allows the system to handle thousands of concurrent connections with minimal memory footprint (~2KB per routine).

-----

## How to Run (Docker Method)

**Recommended.** Requires [Docker Desktop](https://www.docker.com/products/docker-desktop/) installed.

### 1\. The "Happy Path" (Standard Operation)

Run this configuration to demonstrate normal system behavior.

  * **Config:** 2 Workers, Queue size 5, 2 Producers.

<!-- end list -->

```bash
docker-compose -f docker-compose.test1.yml up --build
```

**Verify:**

1.  Open your browser to [http://localhost:8080](localhost:8080).
2.  Watch the video appear, hover to see the GIF preview, click to play.

### 2\. The "Stress Test" (Leaky Bucket Demo)

Run this configuration to demonstrate the queue overflow protection.

  * **Config:** 2 Workers, Queue size 5, 100 Aggressive Producers.

<!-- end list -->

```bash
docker-compose -f docker-compose.test2.yml up --build
```

**Verify:**

1.  Check the terminal logs.
2.  You will see `Queue Full` errors on the Server and `Server Busy` errors on the Client.
3.  This proves the Leaky Bucket is dropping excess traffic.

### 3\. Custom Scenarios

You can override any setting using environment variables with the standard compose file.

```bash
# Example: Run with 50 concurrent producers
CLIENT_P=50 docker-compose up --build
```

-----

## How to Run (Manual Method)

Use this if you do not have Docker.
**Prerequisites:** Go 1.25.5+, FFmpeg.

### 1\. Build

```bash
go build -o server ./cmd/server
go build -o client ./cmd/client
```

### 2\. Run Server

```bash
# -c: Workers, -q: Queue Size
./server -c 2 -q 5
```

### 3\. Run Client (New Terminal)

```bash
# -p: Concurrent Producers
./client -p 3
```

-----

## Performance & Benchmarks

All benchmarks were conducted on a local development environment (Docker/Linux) using the built-in `client` load generator.

### 1. High-Throughput Data Ingestion
**Scenario:** 4 Concurrent Video Streams (Happy Path).
* **Throughput:** **~129 MB/s**
* **Latency:** Processed 4MB of video data in **31ms**.
* **Protocol Efficiency:** <1% overhead using gRPC binary streaming vs. ~33% overhead for REST/JSON (Base64).

> **Proof:**
> `client-1 | INFO All producers finished duration=31.364209ms`

### 2. Concurrency & Vertical Scaling
**Scenario:** 100 Concurrent Producers (200 Total Requests) with Scaled Resources (`-c 10 -q 50`).
* **Requests Per Second (RPS):** **~428 RPS**
* **Handling Time:** Handled 200 concurrent connection attempts (accepting or rejecting) in **0.46 seconds**.
* **Context:** Proves the non-blocking architecture allows the main thread to accept/reject connections almost instantly, preventing thread starvation.

> **Proof:**
> `client-1 | INFO All producers finished duration=467.730125ms`

### 3. Resilience (Load Shedding)
**Scenario:** System under DDoS-like load (100 concurrent requests > 5 Queue Slots).
The system demonstrates **Fail-Fast** behavior. Instead of crashing or slowing down, the Leaky Bucket immediately rejects excess traffic with `gRPC Code: ResourceExhausted`, preserving CPU for active jobs.

**Log Evidence:**
```text
[Server] WARN Queue full, dropping video id=video_vid_98_1.mp4
[Client] ERROR Upload failed producer=98 error="rpc error: code = ResourceExhausted"
[Server] INFO Processing Video worker=1 file=uploads/video_vid_96_1.mp4
```

The server continued processing `vid_96_1` successfully while rejecting `vid_98_1`, proving isolation between the Ingestion Layer and Worker Layer.

-----

## Project Structure

```text
media-upload/
├── cmd/
│   ├── server/              # Main entry point for Cloud Server (gRPC + HTTP)
│   └── client/              # Main entry point for Edge Client (Producer)
├── internal/
│   └── server/              # Core Logic: Worker Pool & Leaky Bucket implementation
├── pb/                      # Generated gRPC/Protobuf code
├── proto/                   # Shared Protocol definition (.proto)
├── static/                  # Frontend: HTML Dashboard
├── Dockerfile.server        # Server container (includes FFmpeg)
├── Dockerfile.client        # Client container
├── docker-compose.yml       # Dynamic configuration
├── docker-compose.test1.yml # Test scenario 1
└── docker-compose.test2.yml # Test scenario 2
```

## Configuration Flags

Both binaries accept command-line flags for tuning:

**Server:**

  * `-c`: Number of consumer worker threads (Default: 2).
  * `-q`: Max queue length (Leaky Bucket size) (Default: 5).
  * `-port`: gRPC port (Default: 50051).
  * `-http`: HTTP port (Default: 8080).

**Client:**

  * `-p`: Number of producer threads (Default: 1).
  * `-addr`: Address of the server (Default: "127.0.0.1:50051").
  * `-dir`: Directory to store/read dummy video data.
