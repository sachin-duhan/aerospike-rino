package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	vegeta "github.com/tsenart/vegeta/v12/lib"
)

type KYCRequest struct {
	JourneyID string `json:"journey_id"`
	Image     string `json:"image"`
}

var (
	rate      = flag.Int("rate", 100, "Number of requests per second")
	duration  = flag.Duration("duration", 1*time.Minute, "Duration of the test")
	images    []string
	targetURL = "http://localhost:8080/api/kyc"
	imageDir  = "images"
)

// loadImages loads and base64 encodes all images from the images directory
func loadImages() error {
	log.Printf("Loading images from directory: %s", imageDir)
	files, err := os.ReadDir(imageDir)
	if err != nil {
		return fmt.Errorf("failed to read image directory: %v", err)
	}

	var totalSize int64
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		if filepath.Ext(file.Name()) != ".jpg" && filepath.Ext(file.Name()) != ".jpeg" && filepath.Ext(file.Name()) != ".png" {
			continue
		}

		path := filepath.Join(imageDir, file.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read image %s: %v", file.Name(), err)
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		images = append(images, encoded)
		totalSize += int64(len(data))

		log.Printf("Loaded image: %s, size: %d bytes", file.Name(), len(data))
	}

	if len(images) == 0 {
		return fmt.Errorf("no images found in %s", imageDir)
	}

	log.Printf("Successfully loaded %d images, total size: %d bytes", len(images), totalSize)
	return nil
}

// createTargeter creates a request targeter for vegeta
func createTargeter() vegeta.Targeter {
	requestCount := 0
	startTime := time.Now()

	return func(tgt *vegeta.Target) error {
		if len(images) == 0 {
			return fmt.Errorf("no images available")
		}

		requestCount++
		if requestCount%1000 == 0 {
			elapsed := time.Since(startTime)
			rate := float64(requestCount) / elapsed.Seconds()
			log.Printf("Generated %d requests, current rate: %.2f req/sec", requestCount, rate)
		}

		// Create a random journey ID and select a random image
		journeyID := fmt.Sprintf("journey_%d", rand.Int63())
		imageIndex := rand.Intn(len(images))
		image := images[imageIndex]

		// Create the request payload
		payload := KYCRequest{
			JourneyID: journeyID,
			Image:     image,
		}

		jsonPayload, err := json.Marshal(payload)
		if err != nil {
			return err
		}

		tgt.Method = "POST"
		tgt.URL = targetURL
		tgt.Body = jsonPayload
		tgt.Header = make(map[string][]string)
		tgt.Header["Content-Type"] = []string{"application/json"}

		return nil
	}
}

func main() {
	log.Printf("Starting KYC Load Test...")
	flag.Parse()

	log.Printf("Configuration:")
	log.Printf("- Target URL: %s", targetURL)
	log.Printf("- Request Rate: %d/sec", *rate)
	log.Printf("- Duration: %v", *duration)

	// Load images
	if err := loadImages(); err != nil {
		log.Fatalf("Failed to load images: %v", err)
	}

	// Initialize attack
	rate := vegeta.Rate{Freq: *rate, Per: time.Second}
	duration := *duration
	targeter := createTargeter()
	attacker := vegeta.NewAttacker()

	startTime := time.Now()

	// Run the attack
	var metrics vegeta.Metrics
	for res := range attacker.Attack(targeter, rate, duration, "KYC Load Test") {
		metrics.Add(res)
	}
	metrics.Close()

	elapsed := time.Since(startTime)
	log.Printf("Load test completed in %v", elapsed)

	// Create report
	log.Printf("\nDetailed Results:")
	report := vegeta.NewTextReporter(&metrics)
	report.Report(os.Stdout)

	// Additional custom metrics
	fmt.Printf("\nCustom Metrics:\n")
	fmt.Printf("Total Requests: %d\n", metrics.Requests)
	fmt.Printf("Success Rate: %.2f%%\n", metrics.Success*100)
	fmt.Printf("Mean Latency: %s\n", metrics.Latencies.Mean)
	fmt.Printf("99th Percentile Latency: %s\n", metrics.Latencies.P99)
	fmt.Printf("Max Latency: %s\n", metrics.Latencies.Max)
	fmt.Printf("Throughput: %.2f req/sec\n", metrics.Throughput)

	// Print error summary if any
	if metrics.Success < 1.0 {
		log.Printf("\nError Summary:")
		errorSet := make(map[string]int)
		for _, result := range metrics.Errors {
			errorSet[result] += 1
		}
		for err, count := range errorSet {
			log.Printf("- %s: %d occurrences", err, count)
		}
	}
}
